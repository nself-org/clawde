/**
 * Purpose: ClawDE-specific typed error union and Result<T,E> wrappers.
 * Inputs:  None — pure type definitions + trivial helpers.
 * Outputs: ClawDEError discriminated union, Result<T,E>, ok/err constructors.
 * Constraints: ClawDE-local errors only; cross-surface errors use @nself/errors AppError.
 *              No untyped throw — every fallible operation returns Result<T, ClawDEError>.
 * SPORT: T-P3-E5-W1-S2-T02
 */

// ── ClawDE-specific error codes ────────────────────────────────────────────────

export type ClawDEErrorCode =
  | 'daemon_offline'
  | 'lsp_error'
  | 'oauth_expired'
  | 'file_permission'
  | 'api_error'
  | 'unknown';

interface ClawDEErrorBase {
  readonly code: ClawDEErrorCode;
  readonly message: string;
}

/** Daemon process is not running or unreachable. */
export interface DaemonOfflineError extends ClawDEErrorBase {
  readonly code: 'daemon_offline';
}

/** Language Server Protocol bridge encountered a protocol-level failure. */
export interface LspError extends ClawDEErrorBase {
  readonly code: 'lsp_error';
  /** The raw LSP error code if available. */
  readonly lspCode?: number;
}

/** OAuth token for a provider account has expired or been revoked. */
export interface OAuthExpiredError extends ClawDEErrorBase {
  readonly code: 'oauth_expired';
  readonly provider: OAuthProvider;
  readonly accountId: string;
}

/** File-system operation failed due to missing read/write permission. */
export interface FilePermissionError extends ClawDEErrorBase {
  readonly code: 'file_permission';
  readonly path: string;
}

/** REST/RPC call to the daemon API returned a non-2xx status. */
export interface ApiError extends ClawDEErrorBase {
  readonly code: 'api_error';
  readonly status: number;
}

/** Catch-all for unexpected errors that don't fit a known code. */
export interface UnknownError extends ClawDEErrorBase {
  readonly code: 'unknown';
  readonly cause?: unknown;
}

/** Canonical ClawDE error union — use as E in Result<T, ClawDEError>. */
export type ClawDEError =
  | DaemonOfflineError
  | LspError
  | OAuthExpiredError
  | FilePermissionError
  | ApiError
  | UnknownError;

// ── Constructor helpers ────────────────────────────────────────────────────────

export const daemonOffline = (message: string): DaemonOfflineError =>
  ({ code: 'daemon_offline', message });

export const lspError = (message: string, lspCode?: number): LspError =>
  ({ code: 'lsp_error', message, ...(lspCode !== undefined ? { lspCode } : {}) });

export const oauthExpired = (
  message: string,
  provider: OAuthProvider,
  accountId: string
): OAuthExpiredError =>
  ({ code: 'oauth_expired', message, provider, accountId });

export const filePermission = (message: string, path: string): FilePermissionError =>
  ({ code: 'file_permission', message, path });

export const apiError = (message: string, status: number): ApiError =>
  ({ code: 'api_error', message, status });

export const unknownError = (message: string, cause?: unknown): UnknownError =>
  ({ code: 'unknown', message, cause });

/** Convert any thrown value into a ClawDEError. */
export function fromThrown(err: unknown): ClawDEError {
  if (err instanceof Error) {
    return unknownError(err.message, err);
  }
  return unknownError(String(err), err);
}

// ── Result<T,E> ───────────────────────────────────────────────────────────────

/** Successful result wrapper. */
export type Ok<T> = { readonly _tag: 'Ok'; readonly value: T };

/** Error result wrapper. */
export type Err<E> = { readonly _tag: 'Err'; readonly error: E };

/**
 * Result<T, E> — canonical return type for all fallible ClawDE operations.
 * Narrow with: `if (result._tag === 'Ok') { result.value } else { result.error }`
 */
export type Result<T, E = ClawDEError> = Ok<T> | Err<E>;

/** Construct a successful Result. */
export const ok = <T>(value: T): Ok<T> => ({ _tag: 'Ok', value });

/** Construct a failure Result. */
export const err = <E>(error: E): Err<E> => ({ _tag: 'Err', error });

/** Type guard: true if result is Ok. */
export const isOk = <T, E>(result: Result<T, E>): result is Ok<T> =>
  result._tag === 'Ok';

/** Type guard: true if result is Err. */
export const isErr = <T, E>(result: Result<T, E>): result is Err<E> =>
  result._tag === 'Err';

/** Extract Ok value or return a fallback for Err. */
export const getOrElse = <T, E>(result: Result<T, E>, fallback: T): T =>
  isOk(result) ? result.value : fallback;

/** Pattern-match both branches of a Result. */
export const match = <T, E, U>(
  result: Result<T, E>,
  onOk: (value: T) => U,
  onErr: (error: E) => U
): U => (isOk(result) ? onOk(result.value) : onErr(result.error));

// ── OAuth provider type (used by OAuthExpiredError) ───────────────────────────

/** Re-exported here for use in error constructors; canonical definition in @/types. */
export type OAuthProvider = 'google' | 'github' | 'anthropic';
