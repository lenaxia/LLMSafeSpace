/**
 * Static analysis: Rules of Hooks — no hook calls after early returns.
 *
 * React requires hooks to be called in the same order on every render.
 * A hook defined AFTER an early `return` statement is only called when the
 * early return is NOT taken, causing a different hook count between renders
 * and throwing React error #310 ("Rendered more/fewer hooks than during the
 * previous render").
 *
 * This test suite scans every .tsx / .ts source file and asserts that no
 * built-in React hook call (useState, useEffect, useRef, useCallback,
 * useMemo, useLayoutEffect, useReducer, useContext, useImperativeHandle,
 * useDebugValue, useId, useDeferredValue, useTransition, useSyncExternalStore)
 * appears after the first early return inside any exported function body.
 *
 * It also asserts that no hook call appears inside a conditional block
 * (if/else) or loop (for/while).
 *
 * History: React error #310 was introduced in feat #69 (message queue) and
 * found in DiskUsageBar.tsx during the same audit. Both were fixed before
 * this test was written.
 */
import { describe, it, expect } from "vitest";
import * as fs from "fs";
import * as path from "path";
import * as glob from "glob";

// ── constants ──────────────────────────────────────────────────────────────

const SRC_DIR = path.resolve(__dirname, "..");

// Built-in React hooks. Custom hooks (use*) are intentionally excluded to
// avoid false positives — they appear in mock files, type definitions, etc.
const BUILTIN_HOOK_RE =
  /\b(useState|useEffect|useRef|useCallback|useMemo|useLayoutEffect|useReducer|useContext|useImperativeHandle|useDebugValue|useId|useDeferredValue|useTransition|useSyncExternalStore)\s*[<(]/;

// An early return: `return` followed by a value or JSX on the same line, or
// `return (` that begins a multi-line block. We use a line-by-line heuristic:
// any line whose trimmed content starts with "return " or "return(" or
// "return null" or "return undefined" and is inside a function body.
const EARLY_RETURN_LINE_RE = /^\s*return\b/;

// Lines to skip: pure comments, import statements, type definitions, etc.
const SKIP_LINE_RE = /^\s*(\/\/|\/\*|\*|import |export type |type |interface )/;

// ── helpers ────────────────────────────────────────────────────────────────

interface Violation {
  file: string;
  kind: "hook-after-early-return" | "hook-in-conditional";
  hookName: string;
  hookLine: number;
  returnLine?: number;
  detail: string;
}

/**
 * Very lightweight heuristic parser.  Does NOT handle all edge cases (e.g.
 * multi-line ternaries, JSX that looks like code) but is good enough to catch
 * the class of bugs that caused the production incident.
 *
 * Strategy:
 *   - Track nesting depth with `{` / `}`.
 *   - Within each top-level function body (depth 1 relative to the function
 *     open brace) look for `return` statements.
 *   - After the first `return` in a function, any `use*` call at the same
 *     depth is a violation.
 *   - Any `use*` call inside an `if` / `else` / `for` / `while` block at
 *     depth > function-body-depth is a conditional-hook violation.
 */
function analyzeFile(filePath: string): Violation[] {
  const violations: Violation[] = [];
  const src = fs.readFileSync(filePath, "utf-8");
  const lines = src.split("\n");

  // State machine per "function context"
  // We push a new context when we see a function/arrow/component declaration
  // and pop when its closing brace is found.
  interface FnCtx {
    openBraceLine: number; // line where the function body opened
    depth: number;         // brace depth at function body open
    firstReturnLine: number | null;
    seenHooks: boolean;
  }

  const fnStack: FnCtx[] = [];
  let depth = 0; // global brace depth

  // Very simple function-start detector: lines containing `function `, `=> {`
  // or React component patterns.  Not perfect but catches real cases.
  const FN_START_RE = /(?:function\s+\w|=>\s*\{|function\s*\()/;

  for (let i = 0; i < lines.length; i++) {
    const line: string = lines[i] ?? "";
    const lineNum = i + 1;

    if (SKIP_LINE_RE.test(line)) continue;

    // Count braces on this line (crude — doesn't handle strings/templates)
    const opens = (line.match(/\{/g) ?? []).length;
    const closes = (line.match(/\}/g) ?? []).length;

    const depthBefore = depth;
    depth += opens - closes;

    // Detect function start: opening brace on a line with a function keyword
    if (opens > closes && FN_START_RE.test(line)) {
      fnStack.push({
        openBraceLine: lineNum,
        depth: depthBefore + 1, // depth inside function body
        firstReturnLine: null,
        seenHooks: false,
      });
    }

    // Pop function contexts that have been closed
    while (fnStack.length > 0 && depth < (fnStack[fnStack.length - 1]?.depth ?? 0)) {
      fnStack.pop();
    }

    if (fnStack.length === 0) continue;
    const ctx: FnCtx | undefined = fnStack[fnStack.length - 1];
    if (!ctx) continue;

    // Check for early return (at the function's own depth level)
    if (
      depth === ctx.depth &&
      EARLY_RETURN_LINE_RE.test(line) &&
      ctx.firstReturnLine === null
    ) {
      ctx.firstReturnLine = lineNum;
    }

    // Check for hook call
    const hookMatch = line.match(BUILTIN_HOOK_RE);
    if (hookMatch) {
      const hookName: string = hookMatch[1] ?? "";

      // Violation: hook after early return at the same function depth
      if (ctx.firstReturnLine !== null && depth === ctx.depth) {
        violations.push({
          file: filePath,
          kind: "hook-after-early-return",
          hookName,
          hookLine: lineNum,
          returnLine: ctx.firstReturnLine,
          detail: `${hookName}() on line ${lineNum} is after early return on line ${ctx.firstReturnLine}`,
        });
      }

      // Violation: hook inside a conditional / loop (depth > function body)
      // We check for `if` / `else` / `for` / `while` in a small lookback window.
      if (depth > ctx.depth) {
        // Look back up to 15 lines for an opening conditional/loop keyword
        const windowStart = Math.max(0, i - 15);
        const window = lines.slice(windowStart, i + 1).join("\n");
        if (/\b(if|else|for|while)\b.*\{/.test(window)) {
          violations.push({
            file: filePath,
            kind: "hook-in-conditional",
            hookName,
            hookLine: lineNum,
            detail: `${hookName}() on line ${lineNum} appears to be inside a conditional/loop block`,
          });
        }
      }
    }
  }

  return violations;
}

// ── test ───────────────────────────────────────────────────────────────────

describe("Rules of Hooks — static analysis across all source files", () => {
  const files = glob.sync("**/*.{ts,tsx}", {
    cwd: SRC_DIR,
    absolute: true,
    ignore: [
      "**/*.test.{ts,tsx}",
      "**/*.spec.{ts,tsx}",
      "**/test/**",
      "**/node_modules/**",
      "**/__mocks__/**",
    ],
  });

  it("finds at least one source file to analyze", () => {
    expect(files.length).toBeGreaterThan(10);
  });

  it("no built-in hook calls after an early return in any component or hook", () => {
    const allViolations: Violation[] = [];

    for (const file of files) {
      const violations = analyzeFile(file).filter(
        (v) => v.kind === "hook-after-early-return",
      );
      allViolations.push(...violations);
    }

    if (allViolations.length > 0) {
      const msg = allViolations
        .map(
          (v) =>
            `  ${path.relative(SRC_DIR, v.file)}: ${v.detail}`,
        )
        .join("\n");
      expect.fail(
        `Found ${allViolations.length} Rules of Hooks violation(s) — hook called after early return:\n${msg}\n\n` +
        `This causes React error #310 in production. Move the hook above the early return.`,
      );
    }
  });

  it("no built-in hook calls inside conditional or loop blocks", () => {
    const allViolations: Violation[] = [];

    for (const file of files) {
      const violations = analyzeFile(file).filter(
        (v) => v.kind === "hook-in-conditional",
      );
      allViolations.push(...violations);
    }

    if (allViolations.length > 0) {
      const msg = allViolations
        .map(
          (v) =>
            `  ${path.relative(SRC_DIR, v.file)}: ${v.detail}`,
        )
        .join("\n");
      expect.fail(
        `Found ${allViolations.length} Rules of Hooks violation(s) — hook inside conditional/loop:\n${msg}\n\n` +
        `Hooks must be called unconditionally on every render.`,
      );
    }
  });
});
