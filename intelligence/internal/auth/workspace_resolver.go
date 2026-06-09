// Package auth — workspace resolver: maps JWT claims to clawde_workspaces rows.
//
// Purpose: Given validated JWT Claims, resolve the workspace row from the DB.
//          LOCKED RULE: if claims.WorkspaceID is present (claim clawde/workspace_id)
//          but no matching clawde_workspaces row exists → return ErrWorkspaceNotFound
//          (caller maps this to 401). Auto-create path exists ONLY for the
//          claims.Sub fallback (when workspace_id claim is absent).
//
// Inputs:  *Claims from JWTValidator.Validate; *pgx.Conn or pgxpool.Pool.
// Outputs: *Workspace on success; sentinel error on not-found.
// Constraints: Sets GUC app.workspace_id so RLS filters apply to all queries
//              within the returned transaction context. Never auto-creates a
//              workspace when workspace_id claim is present.
// SPORT: REGISTRY-FUNCTIONS.md — WorkspaceResolver.
package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrWorkspaceNotFound is returned when a workspace_id claim is present in the
// JWT but no matching clawde_workspaces row exists in the database.
// Callers should map this to HTTP 401 / gRPC Unauthenticated.
var ErrWorkspaceNotFound = errors.New("workspace not found")

// Workspace holds the resolved workspace fields needed by downstream handlers.
type Workspace struct {
	ID      string
	OwnerID string
	Name    string
}

// WorkspaceResolver resolves JWT claims to workspace rows.
type WorkspaceResolver struct {
	pool *pgxpool.Pool
}

// NewWorkspaceResolver creates a WorkspaceResolver backed by the given pool.
func NewWorkspaceResolver(pool *pgxpool.Pool) *WorkspaceResolver {
	return &WorkspaceResolver{pool: pool}
}

// Resolve maps claims to a Workspace, enforcing the locked rule:
//
//   - If claims.WorkspaceID is set: look up clawde_workspaces by id.
//     If not found → return ErrWorkspaceNotFound (→ 401).
//     Do NOT auto-create.
//
//   - If claims.WorkspaceID is empty: look up by owner_id = claims.Sub.
//     If not found → auto-create a workspace row with owner_id = claims.Sub.
//
// After resolution, sets the GUC app.workspace_id on conn so RLS policies
// applied to all subsequent queries in the same transaction scope see the
// correct workspace.
func (r *WorkspaceResolver) Resolve(ctx context.Context, claims *Claims) (*Workspace, error) {
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("workspace resolver: acquire conn: %w", err)
	}
	defer conn.Release()

	var ws *Workspace

	if claims.WorkspaceID != "" {
		// Explicit workspace_id claim — must exist, never auto-create.
		ws, err = lookupByID(ctx, conn.Conn(), claims.WorkspaceID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrWorkspaceNotFound
			}
			return nil, fmt.Errorf("workspace resolver: lookup by id: %w", err)
		}
	} else {
		// Sub-only fallback — auto-create if not found.
		ws, err = lookupByOwner(ctx, conn.Conn(), claims.Sub)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				ws, err = createWorkspace(ctx, conn.Conn(), claims.Sub)
				if err != nil {
					return nil, fmt.Errorf("workspace resolver: auto-create: %w", err)
				}
			} else {
				return nil, fmt.Errorf("workspace resolver: lookup by owner: %w", err)
			}
		}
	}

	// Set GUC so RLS policies (app.workspace_id) apply for this connection.
	if _, err := conn.Exec(ctx, `SELECT set_config('app.workspace_id', $1, false)`, ws.ID); err != nil {
		return nil, fmt.Errorf("workspace resolver: set_config: %w", err)
	}

	return ws, nil
}

func lookupByID(ctx context.Context, conn *pgx.Conn, id string) (*Workspace, error) {
	var ws Workspace
	err := conn.QueryRow(ctx,
		`SELECT id, owner_id, name FROM clawde_workspaces WHERE id = $1`,
		id,
	).Scan(&ws.ID, &ws.OwnerID, &ws.Name)
	if err != nil {
		return nil, err
	}
	return &ws, nil
}

func lookupByOwner(ctx context.Context, conn *pgx.Conn, ownerID string) (*Workspace, error) {
	var ws Workspace
	err := conn.QueryRow(ctx,
		`SELECT id, owner_id, name FROM clawde_workspaces WHERE owner_id = $1 LIMIT 1`,
		ownerID,
	).Scan(&ws.ID, &ws.OwnerID, &ws.Name)
	if err != nil {
		return nil, err
	}
	return &ws, nil
}

func createWorkspace(ctx context.Context, conn *pgx.Conn, ownerID string) (*Workspace, error) {
	var ws Workspace
	err := conn.QueryRow(ctx,
		`INSERT INTO clawde_workspaces (owner_id, name)
		 VALUES ($1, $1)
		 RETURNING id, owner_id, name`,
		ownerID,
	).Scan(&ws.ID, &ws.OwnerID, &ws.Name)
	if err != nil {
		return nil, err
	}
	return &ws, nil
}
