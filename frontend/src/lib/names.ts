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
