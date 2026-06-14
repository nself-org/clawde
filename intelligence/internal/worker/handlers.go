// handlers.go — Default handler map wiring for the clawde worker Pool.
//
// Purpose: Register embed, analyze, and ingest handlers for the three
//          canonical pgmq queues. Each handler adapts the worker.Handler
//          signature to the respective package API.
//          Embed handler is a stub (TODO: wire to embedding pipeline when ready).
//          Analyze handler delegates to staticanalysis.Runner.Handle.
//          Ingest handler delegates to docs.KBIngestor.IngestDocURL.
// Inputs:  *staticanalysis.Runner, *docs.KBIngestor (may be nil — guarded).
// Outputs: map[string]Handler ready for Config.Handlers.
// Constraints: File ≤150 lines. No panic on nil runner or ingestor.
//
// SPORT: REGISTRY-FUNCTIONS.md — worker.Pool.Start, worker.Pool.Stop.
package worker

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/nself-org/clawde/intelligence/internal/docs"
	"github.com/nself-org/clawde/intelligence/internal/staticanalysis"
)

// DefaultHandlers builds the canonical handler map for the three core queues.
// Nil runner or ingestor produces a logged stub (does not panic, does not retry).
func DefaultHandlers(
	runner *staticanalysis.Runner,
	ingestor *docs.KBIngestor,
	logger *slog.Logger,
) map[string]Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return map[string]Handler{
		QueueEmbed:   embedHandler(logger),
		QueueAnalyze: analyzeHandler(runner, logger),
		QueueIngest:  ingestHandler(ingestor, logger),
	}
}

// embedHandler returns a stub Handler for QueueEmbed.
// TODO: replace stub with embedding.HandleEmbedJob when the embedding pipeline
//       is wired in a subsequent ticket (P2-E1-W2-S3+).
func embedHandler(logger *slog.Logger) Handler {
	return func(ctx context.Context, msg *Message) error {
		logger.Info("embed handler invoked (stub — TODO: wire embedding pipeline)",
			"job_id", msg.JobID,
			"queue", msg.Queue,
		)
		// Return nil to ack the message. Stub does not fail so it does not
		// land in the DLQ and block queue drain during development.
		return nil
	}
}

// analyzeHandler adapts staticanalysis.Runner.Handle to worker.Handler.
// Runner.Handle takes raw []byte; we pass msg.Payload directly.
func analyzeHandler(runner *staticanalysis.Runner, logger *slog.Logger) Handler {
	return func(ctx context.Context, msg *Message) error {
		if runner == nil {
			logger.Warn("analyze handler: runner is nil — skipping",
				"job_id", msg.JobID)
			return nil
		}
		if err := runner.Handle(ctx, []byte(msg.Payload)); err != nil {
			return err
		}
		return nil
	}
}

// ingestHandler adapts docs.KBIngestor.IngestDocURL to worker.Handler.
// Payload is expected to be an IngestDocURLRequest JSON with an additional
// client_id field. Missing fields produce a logged stub pass-through.
func ingestHandler(ingestor *docs.KBIngestor, logger *slog.Logger) Handler {
	return func(ctx context.Context, msg *Message) error {
		if ingestor == nil {
			logger.Warn("ingest handler: ingestor is nil — skipping",
				"job_id", msg.JobID)
			return nil
		}

		// Decode minimal fields from the pgmq message payload.
		var p struct {
			ClientID    string `json:"client_id"`
			WorkspaceID string `json:"workspace_id"`
			URL         string `json:"url"`
			DocType     string `json:"doc_type"`
		}
		if err := jsonUnmarshal(msg.Payload, &p); err != nil {
			logger.Warn("ingest handler: bad payload — skipping",
				"job_id", msg.JobID, "err", err)
			// Return nil: malformed payload should not retry forever.
			return nil
		}
		if p.URL == "" || p.WorkspaceID == "" {
			logger.Warn("ingest handler: missing url or workspace_id — skipping",
				"job_id", msg.JobID)
			return nil
		}

		clientID := p.ClientID
		if clientID == "" {
			clientID = "worker-pool"
		}

		_, err := ingestor.IngestDocURL(ctx, clientID, docs.IngestDocURLRequest{
			WorkspaceID: p.WorkspaceID,
			URL:         p.URL,
			DocType:     p.DocType,
		})
		return err
	}
}

// jsonUnmarshal is a local alias for encoding/json.Unmarshal.
func jsonUnmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
