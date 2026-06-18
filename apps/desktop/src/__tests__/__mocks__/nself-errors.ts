/**
 * Purpose: Jest mock for @nself/errors — provides Result helpers as pure JS.
 * Constraints: No workspace source imports; mirrors the real types exactly.
 * SPORT: T-P3-E5-W1-S2-T01
 */

export type Ok<T> = { readonly _tag: "Ok"; readonly value: T };
export type Err<E> = { readonly _tag: "Err"; readonly error: E };
export type Result<T, E = AppError> = Ok<T> | Err<E>;

export const ok = <T>(value: T): Ok<T> => ({ _tag: "Ok", value });
export const err = <E>(error: E): Err<E> => ({ _tag: "Err", error });
export const isOk = <T, E>(r: Result<T, E>): r is Ok<T> => r._tag === "Ok";
export const isErr = <T, E>(r: Result<T, E>): r is Err<E> => r._tag === "Err";

// AppError shape subset used in tests
export interface AppError {
  readonly code: string;
  readonly message: string;
  readonly status: number;
}

export const makeAppError = (code: string, message = code, status = 500): AppError => ({
  code,
  message,
  status,
});
