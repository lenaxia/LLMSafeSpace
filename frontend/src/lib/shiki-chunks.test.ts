import { describe, it, expect } from "vitest";
import { CORE_CHUNK_PATTERN } from "./shiki-chunks";

/**
 * The CacheFirst urlPattern in vite.config.ts uses CORE_CHUNK_PATTERN to
 * exclude core app chunks from the shiki-chunks cache. These tests verify
 * that the regex correctly identifies which files are core (precached) and
 * which are Shiki grammar/theme chunks (CacheFirst).
 *
 * If any of these tests fail after a vite.config.ts change, update both
 * globPatterns and CORE_CHUNK_PATTERN to stay in sync.
 */

const EXCLUDED_FROM_CACHE_FIRST = [
  "/assets/index-BLJz3XSy.js",
  "/assets/index-106IglK_.js",
  "/assets/vendor-DAxIHGAX.js",
  "/assets/query-BLbqEaBk.js",
  "/assets/workbox-window.prod.es5-BqEJf4Xk.js",
  "/assets/workerBundle-DGWlUuev.js",
];

const HANDLED_BY_CACHE_FIRST = [
  "/assets/python-B6aJPvgy.js",
  "/assets/typescript-BPQ3VLAy.js",
  "/assets/go-C27-OAKa.js",
  "/assets/github-dark-DHJKELXO.js",
  "/assets/tokyo-night-hegEt444.js",
  "/assets/emacs-lisp-CXvaQtF9.js",
  "/assets/cpp-UfJy6YNI.js",
];

function urlPattern(pathname: string): boolean {
  return (
    pathname.startsWith("/assets/") &&
    pathname.endsWith(".js") &&
    !CORE_CHUNK_PATTERN.test(pathname)
  );
}

describe("CORE_CHUNK_PATTERN — core chunks excluded from CacheFirst", () => {
  it.each(EXCLUDED_FROM_CACHE_FIRST)("excludes %s", (path) => {
    expect(urlPattern(path)).toBe(false);
  });
});

describe("CORE_CHUNK_PATTERN — Shiki chunks handled by CacheFirst", () => {
  it.each(HANDLED_BY_CACHE_FIRST)("includes %s", (path) => {
    expect(urlPattern(path)).toBe(true);
  });
});

describe("CORE_CHUNK_PATTERN — edge cases", () => {
  it("does not match CSS files", () => {
    expect(urlPattern("/assets/index-Og9bDQGC.css")).toBe(false);
  });

  it("does not match files outside /assets/", () => {
    expect(urlPattern("/sw.js")).toBe(false);
    expect(urlPattern("/workbox-9d79bed4.js")).toBe(false);
  });
});
