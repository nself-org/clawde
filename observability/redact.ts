/**
 * Purpose: PII redaction for nClaw observability trace payloads before persistence.
 *   Applies four regex patterns (email, US phone, SSN, proper-noun bigram with title prefix).
 *   Sets redacted=true on TraceRecord when any pattern matched.
 *   Applied at the observability sink — MUST run before any log export or DB write.
 * Inputs: TraceRecord or arbitrary string payload
 * Outputs: Redacted string + redacted boolean flag; never throws on unrecognized input
 * Constraints: Pure string processing — no I/O. Regex patterns cover US formats.
 *   Name pattern: title prefix (Mr|Ms|Mrs|Dr|Prof) + space + capitalized bigram.
 *   All replaced with clearly identifiable tokens: [EMAIL], [PHONE], [SSN], [NAME].
 * SPORT: F-MASTER.md F-AI-GATEWAY:observability-evals
 */

import type { TraceRecord } from "./types";

// ─── PII patterns ─────────────────────────────────────────────────────────────

/**
 * Email address pattern.
 * Covers standard email formats: user@domain.tld
 * Replacement token: [EMAIL]
 */
export const EMAIL_PATTERN =
  /[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}/g;

/**
 * US phone number pattern.
 * Covers formats: (555) 123-4567, 555-123-4567, 555.123.4567, +1 555 123 4567, 5551234567
 * Replacement token: [PHONE]
 */
export const PHONE_PATTERN =
  /(?:\+1[\s\-.]?)?\(?\d{3}\)?[\s\-.]\d{3}[\s\-.]\d{4}/g;

/**
 * US Social Security Number pattern.
 * Covers formats: 123-45-6789, 123 45 6789
 * Does NOT match standalone 9-digit numbers to avoid false positives.
 * Replacement token: [SSN]
 */
export const SSN_PATTERN = /\b\d{3}[- ]\d{2}[- ]\d{4}\b/g;

/**
 * Proper-noun name pattern (title prefix + capitalized first + last name bigram).
 * Covers: Mr|Ms|Mrs|Dr|Prof followed by space and two capitalized words.
 * Example: "Dr. John Smith", "Ms. Alice Walker"
 * Replacement token: [NAME]
 */
export const NAME_PATTERN =
  /\b(?:Mr|Ms|Mrs|Dr|Prof)\.?\s+[A-Z][a-z]+\s+[A-Z][a-z]+\b/g;

// Ordered application: SSN before PHONE (SSN pattern is more specific)
const PATTERNS: Array<[RegExp, string]> = [
  [SSN_PATTERN, "[SSN]"],
  [EMAIL_PATTERN, "[EMAIL]"],
  [PHONE_PATTERN, "[PHONE]"],
  [NAME_PATTERN, "[NAME]"],
];

// ─── Core redaction function ───────────────────────────────────────────────────

export interface RedactResult {
  /** The string with all PII patterns replaced by tokens */
  redacted: string;
  /** True if at least one pattern matched */
  hadPII: boolean;
  /** Which pattern types matched */
  matchedPatterns: Array<"email" | "phone" | "ssn" | "name">;
}

const PATTERN_NAMES: Array<"ssn" | "email" | "phone" | "name"> = [
  "ssn",
  "email",
  "phone",
  "name",
];

/**
 * Applies all four PII regex patterns to the input string.
 * Returns the redacted string, a hadPII flag, and the list of matched pattern types.
 *
 * @param input - Raw string that may contain PII
 * @returns RedactResult with redacted string and metadata
 */
export function redactString(input: string): RedactResult {
  let current = input;
  const matchedPatterns: Array<"email" | "phone" | "ssn" | "name"> = [];

  PATTERNS.forEach(([pattern, token], idx) => {
    // Reset lastIndex for global regexes
    pattern.lastIndex = 0;
    if (pattern.test(current)) {
      matchedPatterns.push(PATTERN_NAMES[idx]);
      pattern.lastIndex = 0;
      current = current.replace(pattern, token);
    }
    pattern.lastIndex = 0;
  });

  return {
    redacted: current,
    hadPII: matchedPatterns.length > 0,
    matchedPatterns,
  };
}

/**
 * Applies PII redaction to all string fields in a TraceRecord.
 * Sets record.redacted = true if any PII was found.
 *
 * Fields checked: error_code (may contain user content in error messages).
 * Note: trace_id, tenant_id, tool_id, model_id are system-generated identifiers — not redacted.
 * The tool input payload (not stored in TraceRecord directly) must be redacted by the caller
 * before constructing the TraceRecord.
 *
 * @param record - Mutable TraceRecord; modified in-place
 * @returns The same record (modified in-place) for chaining
 */
export function redactTraceRecord(record: TraceRecord): TraceRecord {
  let anyRedacted = false;

  if (record.error_code !== null) {
    const result = redactString(record.error_code);
    if (result.hadPII) {
      record.error_code = result.redacted;
      anyRedacted = true;
    }
  }

  if (anyRedacted) {
    record.redacted = true;
  }

  return record;
}

/**
 * Convenience: redact an arbitrary string payload (e.g. tool input content)
 * before embedding it in a TraceRecord.
 *
 * @param payload - Raw string from tool input or error message
 * @returns { text: string; redacted: boolean }
 */
export function redactPayload(payload: string): {
  text: string;
  redacted: boolean;
} {
  const result = redactString(payload);
  return { text: result.redacted, redacted: result.hadPII };
}
