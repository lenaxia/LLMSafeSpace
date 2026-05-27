import adjectivesRaw from "./words/adjectives.txt?raw";
import nounsRaw from "./words/nouns.txt?raw";

const adjectives = adjectivesRaw.trim().split("\n").map((w) => w.trim()).filter(Boolean);
const nouns = nounsRaw.trim().split("\n").map((w) => w.trim()).filter(Boolean);

export function generateWorkspaceName(): string {
  const adj = adjectives[Math.floor(Math.random() * adjectives.length)]!;
  const noun = nouns[Math.floor(Math.random() * nouns.length)]!;
  const num = Math.floor(Math.random() * 100);
  return `${adj}-${noun}-${num}`;
}

/**
 * Returns the session title if set, or a human-readable fallback based on
 * the last message timestamp. Single canonical implementation used everywhere.
 */
export function sessionDisplayTitle(title: string | undefined, _lastMessageAt: string | undefined): string {
  if (title) return title;
  return "New chat";
}
