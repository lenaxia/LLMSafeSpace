import { useState } from "react";
import type { FormEvent } from "react";
import { Button } from "../ui/Button";
import { Input } from "../ui/Input";

interface CreateWorkspaceParams {
  name: string;
  runtime: string;
}

interface Props {
  onCreate: (params: CreateWorkspaceParams) => void;
  onCancel: () => void;
  loading?: boolean;
}

const RUNTIMES = [
  { value: "python:3.11", label: "Python 3.11" },
  { value: "node:20", label: "Node.js 20" },
  { value: "go:1.22", label: "Go 1.22" },
];

export function NewWorkspaceDialog({ onCreate, onCancel, loading }: Props) {
  const [name, setName] = useState("");
  const [runtime, setRuntime] = useState(RUNTIMES[0]!.value);

  const handleSubmit = (e: FormEvent) => {
    e.preventDefault();
    const trimmed = name.trim();
    if (!trimmed) return;
    onCreate({ name: trimmed, runtime });
  };

  return (
    <form onSubmit={handleSubmit} className="flex flex-col gap-3 p-4">
      <h3 className="text-sm font-semibold">New Workspace</h3>
      <Input
        placeholder="Workspace name"
        value={name}
        onChange={(e) => setName(e.target.value)}
        autoFocus
      />
      <select
        value={runtime}
        onChange={(e) => setRuntime(e.target.value)}
        className="h-9 rounded-md border border-input bg-background px-3 text-sm"
        aria-label="Runtime"
      >
        {RUNTIMES.map((r) => (
          <option key={r.value} value={r.value}>{r.label}</option>
        ))}
      </select>
      <div className="flex justify-end gap-2">
        <Button type="button" variant="ghost" size="sm" onClick={onCancel}>Cancel</Button>
        <Button type="submit" size="sm" disabled={loading || !name.trim()}>
          {loading ? "Creating..." : "Create"}
        </Button>
      </div>
    </form>
  );
}
