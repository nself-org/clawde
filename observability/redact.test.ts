/**
 * Purpose: Unit tests for PII redaction — 5 synthetic fixtures covering the 4 pattern types plus combined.
 * Each fixture exercises a different pattern type and verifies the correct replacement token.
 * SPORT: F-MASTER.md F-AI-GATEWAY:observability-evals
 */

import { redactString, redactPayload, redactTraceRecord } from "./redact";
import type { TraceRecord } from "./types";

function makeTrace(overrides: Partial<TraceRecord> = {}): TraceRecord {
  return {
    trace_id: "trace-redact-001",
    tenant_id: "tenant-001",
    tool_id: "key-001",
    stage: "generate",
    latency_ms: 50,
    token_count: 100,
    cost_usd: 0.001,
    model_id: "claude-haiku-4-5",
    cache_hit: false,
    error_code: null,
    redacted: false,
    ...overrides,
  };
}

// ─── Fixture 1: Email pattern ──────────────────────────────────────────────────
describe("PII Fixture 1 — Email pattern", () => {
  it("redacts email address and returns [EMAIL]", () => {
    const input = "Please contact support at user.name+tag@example.co.uk for help.";
    const result = redactString(input);
    expect(result.redacted).toBe("Please contact support at [EMAIL] for help.");
    expect(result.hadPII).toBe(true);
    expect(result.matchedPatterns).toContain("email");
  });

  it("redacts multiple email addresses in one string", () => {
    const input = "From admin@nself.org to user@domain.com";
    const result = redactString(input);
    expect(result.redacted).toBe("From [EMAIL] to [EMAIL]");
    expect(result.hadPII).toBe(true);
  });
});

// ─── Fixture 2: US Phone pattern ──────────────────────────────────────────────
describe("PII Fixture 2 — US Phone pattern", () => {
  it("redacts formatted US phone number (555) 123-4567 → [PHONE]", () => {
    const input = "Call us at (555) 123-4567 for support.";
    const result = redactString(input);
    expect(result.redacted).toBe("Call us at [PHONE] for support.");
    expect(result.hadPII).toBe(true);
    expect(result.matchedPatterns).toContain("phone");
  });

  it("redacts dashed phone number 555-123-4567 → [PHONE]", () => {
    const input = "Reach me at 555-123-4567.";
    const result = redactString(input);
    expect(result.redacted).toBe("Reach me at [PHONE].");
    expect(result.hadPII).toBe(true);
  });
});

// ─── Fixture 3: SSN pattern ───────────────────────────────────────────────────
describe("PII Fixture 3 — SSN pattern", () => {
  it("redacts SSN 123-45-6789 → [SSN]", () => {
    const input = "Your SSN is 123-45-6789, please verify.";
    const result = redactString(input);
    expect(result.redacted).toBe("Your SSN is [SSN], please verify.");
    expect(result.hadPII).toBe(true);
    expect(result.matchedPatterns).toContain("ssn");
  });

  it("redacts SSN with spaces 123 45 6789 → [SSN]", () => {
    const input = "Social security: 987 65 4321.";
    const result = redactString(input);
    expect(result.redacted).toBe("Social security: [SSN].");
    expect(result.hadPII).toBe(true);
  });
});

// ─── Fixture 4: Name pattern (title prefix bigram) ────────────────────────────
describe("PII Fixture 4 — Name (title-prefix bigram) pattern", () => {
  it("redacts Dr. John Smith → [NAME]", () => {
    const input = "The report was filed by Dr. John Smith yesterday.";
    const result = redactString(input);
    expect(result.redacted).toBe("The report was filed by [NAME] yesterday.");
    expect(result.hadPII).toBe(true);
    expect(result.matchedPatterns).toContain("name");
  });

  it("redacts Ms. Alice Walker → [NAME]", () => {
    const input = "Contact Ms. Alice Walker for details.";
    const result = redactString(input);
    expect(result.redacted).toBe("Contact [NAME] for details.");
    expect(result.hadPII).toBe(true);
  });

  it("does NOT redact plain capitalized bigrams without title prefix", () => {
    const input = "The system admin John Smith updated the config.";
    const result = redactString(input);
    // John Smith without title prefix should NOT be redacted
    expect(result.redacted).toBe("The system admin John Smith updated the config.");
    expect(result.hadPII).toBe(false);
  });
});

// ─── Fixture 5: Combined multiple PII types in one string ─────────────────────
describe("PII Fixture 5 — Combined multiple PII types", () => {
  it("redacts all matched patterns in a mixed-PII string", () => {
    const input =
      "Patient Dr. Jane Doe (SSN: 123-45-6789) can be reached at jane.doe@hospital.org or (555) 987-6543.";
    const result = redactString(input);
    expect(result.hadPII).toBe(true);
    expect(result.matchedPatterns).toContain("ssn");
    expect(result.matchedPatterns).toContain("email");
    expect(result.matchedPatterns).toContain("phone");
    expect(result.matchedPatterns).toContain("name");
    expect(result.redacted).not.toContain("123-45-6789");
    expect(result.redacted).not.toContain("jane.doe@hospital.org");
    expect(result.redacted).not.toContain("987-6543");
    expect(result.redacted).toContain("[SSN]");
    expect(result.redacted).toContain("[EMAIL]");
    expect(result.redacted).toContain("[PHONE]");
    expect(result.redacted).toContain("[NAME]");
  });
});

// ─── No-PII cases ─────────────────────────────────────────────────────────────
describe("Non-PII strings should not be modified", () => {
  it("leaves clean strings unchanged", () => {
    const input = "The retrieval latency was 120ms with 0 errors.";
    const result = redactString(input);
    expect(result.redacted).toBe(input);
    expect(result.hadPII).toBe(false);
    expect(result.matchedPatterns).toEqual([]);
  });
});

// ─── redactPayload ────────────────────────────────────────────────────────────
describe("redactPayload convenience wrapper", () => {
  it("returns redacted text and true flag for PII string", () => {
    const { text, redacted } = redactPayload("User email: test@example.com");
    expect(redacted).toBe(true);
    expect(text).toBe("User email: [EMAIL]");
  });

  it("returns original text and false flag for clean string", () => {
    const { text, redacted } = redactPayload("Latency: 50ms");
    expect(redacted).toBe(false);
    expect(text).toBe("Latency: 50ms");
  });
});

// ─── redactTraceRecord ────────────────────────────────────────────────────────
describe("redactTraceRecord — sets redacted=true when error_code contains PII", () => {
  it("redacts PII in error_code field and sets redacted=true", () => {
    const trace = makeTrace({
      error_code: "User jane@example.com exceeded quota",
    });
    redactTraceRecord(trace);
    expect(trace.redacted).toBe(true);
    expect(trace.error_code).toBe("User [EMAIL] exceeded quota");
  });

  it("does not set redacted=true when no PII in error_code", () => {
    const trace = makeTrace({ error_code: "QUOTA_EXCEEDED" });
    redactTraceRecord(trace);
    expect(trace.redacted).toBe(false);
    expect(trace.error_code).toBe("QUOTA_EXCEEDED");
  });

  it("leaves null error_code unchanged", () => {
    const trace = makeTrace({ error_code: null });
    redactTraceRecord(trace);
    expect(trace.error_code).toBeNull();
    expect(trace.redacted).toBe(false);
  });
});
