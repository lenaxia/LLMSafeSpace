/**
 * Regex that matches the core JS chunks that must be managed by the Workbox
 * precache (not by the CacheFirst runtime route). Any file whose pathname
 * matches this pattern is excluded from the shiki-chunks CacheFirst cache.
 *
 * The two lists that must stay in sync:
 *   1. globPatterns in vite.config.ts   — what gets precached
 *   2. CORE_CHUNK_PATTERN (this file)   — what is excluded from CacheFirst
 *
 * If you add a new manualChunks split, add its filename prefix to both.
 */
export const CORE_CHUNK_PATTERN = /\/(index|vendor|query|workbox-window|workerBundle)[^/]*\.js$/;
