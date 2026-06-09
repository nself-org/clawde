/**
 * Purpose: Validates the golden evaluation dataset JSONL files for schema correctness.
 * Ensures each fixture has all required fields with correct types.
 * Exits non-zero on any validation failure.
 * Inputs: Path arguments or auto-discovers evaluation/datasets/*.jsonl
 * Outputs: Validation report to stdout; non-zero exit on failure
 * Constraints: No live model calls. Pure file I/O + schema validation.
 * SPORT: F-MASTER.md F-AI-GATEWAY:observability-evals
 */

import * as fs from "fs";
import * as path from "path";
import * as readline from "readline";

interface EvalFixture {
  query_id: string;
  query: string;
  retrieval_type: "semantic" | "keyword" | "hybrid";
  expected_top_k_ids: string[];
  minimum_mrr: number;
  ground_truth_source: "human-annotated" | "synthetic";
  tags: string[];
}

const REQUIRED_FIELDS: Array<keyof EvalFixture> = [
  "query_id",
  "query",
  "retrieval_type",
  "expected_top_k_ids",
  "minimum_mrr",
  "ground_truth_source",
  "tags",
];

const VALID_RETRIEVAL_TYPES = new Set(["semantic", "keyword", "hybrid"]);
const VALID_GROUND_TRUTH_SOURCES = new Set(["human-annotated", "synthetic"]);

function validateFixture(
  fixture: unknown,
  lineNum: number,
  file: string
): string[] {
  const errors: string[] = [];
  if (typeof fixture !== "object" || fixture === null) {
    return [`${file}:${lineNum}: not a JSON object`];
  }
  const f = fixture as Record<string, unknown>;

  // Check all required fields present
  for (const field of REQUIRED_FIELDS) {
    if (!(field in f)) {
      errors.push(`${file}:${lineNum}: missing required field '${field}'`);
    }
  }
  if (errors.length > 0) return errors;

  if (typeof f.query_id !== "string" || f.query_id.trim() === "") {
    errors.push(`${file}:${lineNum}: 'query_id' must be a non-empty string`);
  }
  if (typeof f.query !== "string" || f.query.trim() === "") {
    errors.push(`${file}:${lineNum}: 'query' must be a non-empty string`);
  }
  if (!VALID_RETRIEVAL_TYPES.has(f.retrieval_type as string)) {
    errors.push(
      `${file}:${lineNum}: 'retrieval_type' must be one of semantic|keyword|hybrid, got '${f.retrieval_type}'`
    );
  }
  if (
    !Array.isArray(f.expected_top_k_ids) ||
    f.expected_top_k_ids.length === 0
  ) {
    errors.push(
      `${file}:${lineNum}: 'expected_top_k_ids' must be a non-empty array`
    );
  }
  if (
    typeof f.minimum_mrr !== "number" ||
    f.minimum_mrr < 0 ||
    f.minimum_mrr > 1
  ) {
    errors.push(
      `${file}:${lineNum}: 'minimum_mrr' must be a number in [0,1], got '${f.minimum_mrr}'`
    );
  }
  if (!VALID_GROUND_TRUTH_SOURCES.has(f.ground_truth_source as string)) {
    errors.push(
      `${file}:${lineNum}: 'ground_truth_source' must be human-annotated|synthetic, got '${f.ground_truth_source}'`
    );
  }
  if (!Array.isArray(f.tags)) {
    errors.push(`${file}:${lineNum}: 'tags' must be an array`);
  }
  return errors;
}

async function validateFile(
  filePath: string
): Promise<{ errors: string[]; count: number; humanAnnotated: number }> {
  const errors: string[] = [];
  let count = 0;
  let humanAnnotated = 0;
  const seenIds = new Set<string>();

  const rl = readline.createInterface({
    input: fs.createReadStream(filePath),
    crlfDelay: Infinity,
  });

  let lineNum = 0;
  for await (const line of rl) {
    lineNum++;
    if (line.trim() === "") continue;
    let fixture: unknown;
    try {
      fixture = JSON.parse(line);
    } catch (e) {
      errors.push(`${filePath}:${lineNum}: invalid JSON — ${e}`);
      continue;
    }
    const lineErrors = validateFixture(fixture, lineNum, filePath);
    errors.push(...lineErrors);
    if (lineErrors.length === 0) {
      count++;
      const f = fixture as Record<string, unknown>;
      const qid = f.query_id as string;
      if (seenIds.has(qid)) {
        errors.push(`${filePath}:${lineNum}: duplicate query_id '${qid}'`);
      }
      seenIds.add(qid);
      if (f.ground_truth_source === "human-annotated") humanAnnotated++;
    }
  }

  return { errors, count, humanAnnotated };
}

async function main() {
  const datasetsDir = path.join(__dirname, "datasets");
  const files = ["semantic.jsonl", "keyword.jsonl", "hybrid.jsonl"].map((f) =>
    path.join(datasetsDir, f)
  );

  let totalErrors = 0;
  for (const file of files) {
    if (!fs.existsSync(file)) {
      console.error(`MISSING: ${file}`);
      totalErrors++;
      continue;
    }
    const { errors, count, humanAnnotated } = await validateFile(file);
    if (errors.length > 0) {
      console.error(`FAIL: ${file}`);
      for (const e of errors) console.error(`  ${e}`);
      totalErrors += errors.length;
    } else {
      const status = count >= 20 && humanAnnotated >= 10 ? "PASS" : "WARN";
      console.log(
        `${status}: ${path.basename(file)} — ${count} fixtures, ${humanAnnotated} human-annotated`
      );
      if (count < 20)
        console.warn(`  WARNING: only ${count} fixtures (minimum 20 required)`);
      if (humanAnnotated < 10)
        console.warn(
          `  WARNING: only ${humanAnnotated} human-annotated (minimum 10 required)`
        );
    }
  }

  if (totalErrors > 0) {
    console.error(`\nValidation FAILED with ${totalErrors} error(s).`);
    process.exit(1);
  } else {
    console.log("\nAll dataset files validated successfully.");
    process.exit(0);
  }
}

main().catch((e) => {
  console.error("Fatal:", e);
  process.exit(1);
});
