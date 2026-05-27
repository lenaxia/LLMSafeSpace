import { lazy, Suspense } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import rehypeSanitize from "rehype-sanitize";
import { Brain, Wrench, Server } from "lucide-react";
import { cn } from "../../lib/utils";
import type { MessagePart as MessagePartType } from "../../api/types";

const ReactDiffViewer = lazy(() => import("react-diff-viewer-continued"));

function ToolDiffView({ oldStr, newStr }: { oldStr: string; newStr: string }) {
  return (
    <Suspense fallback={<pre className="px-3 text-xs text-muted-foreground">Loading diff...</pre>}>
      <div className="text-xs overflow-auto max-h-60">
        <ReactDiffViewer
          oldValue={oldStr}
          newValue={newStr}
          splitView={false}
          useDarkTheme
          hideLineNumbers={false}
          styles={{
            contentText: { fontSize: "11px", lineHeight: "1.4" },
          }}
        />
      </div>
    </Suspense>
  );
}

interface Props {
  part: MessagePartType;
  isUser: boolean;
  isStreaming?: boolean;
}

export function MessagePart({ part, isUser, isStreaming }: Props) {
  if (part.type === "text" && part.text) {
    if (isUser) {
      return <p className="whitespace-pre-wrap text-sm">{part.text}</p>;
    }
    // During streaming, close any unclosed code fences so markdown renders
    let text = part.text;
    if (isStreaming) {
      const fenceCount = (text.match(/^```/gm) || []).length;
      if (fenceCount % 2 !== 0) {
        text += "\n```";
      }
    }
    return (
      <div className={cn("prose prose-sm dark:prose-invert max-w-none")}>
        <ReactMarkdown remarkPlugins={[remarkGfm]} rehypePlugins={[rehypeSanitize]}>
          {text}
        </ReactMarkdown>
      </div>
    );
  }

  if ((part.type === "thinking" || part.type === "reasoning") && part.text) {
    const content = (
      <div className="border-l-2 border-muted-foreground/30 pl-3 text-xs text-muted-foreground italic">
        <ReactMarkdown remarkPlugins={[remarkGfm]} rehypePlugins={[rehypeSanitize]}>
          {part.text}
        </ReactMarkdown>
      </div>
    );

    if (isStreaming) {
      return (
        <div className="my-2 rounded-md border border-muted-foreground/20 bg-muted/30">
          <div className="flex items-center gap-2 px-3 py-1.5 text-xs font-medium text-muted-foreground">
            <Brain className="h-3.5 w-3.5 animate-pulse" />
            <span>Thinking…</span>
          </div>
          <div className="border-t border-muted-foreground/10 px-3 py-2">
            {content}
          </div>
        </div>
      );
    }

    return (
      <details className="group my-2 rounded-md border border-muted-foreground/20 bg-muted/30">
        <summary className="flex cursor-pointer items-center gap-2 px-3 py-1.5 text-xs font-medium text-muted-foreground hover:text-foreground">
          <Brain className="h-3.5 w-3.5" />
          Thinking
        </summary>
        <div className="border-t border-muted-foreground/10 px-3 py-2">
          {content}
        </div>
      </details>
    );
  }

  if (part.type === "tool_use" || part.type === "tool_call") {
    const toolName = part.name ?? part.text ?? "tool";
    const hasDetails = part.input || part.toolOutput;
    const statusIcon = part.toolState === "completed" ? "✓" : part.toolState === "error" ? "✗" : part.toolState === "running" ? "⟳" : "…";
    const borderColor = part.toolState === "error" ? "border-red-500/20 bg-red-500/5" : "border-blue-500/20 bg-blue-500/5";
    const textColor = part.toolState === "error" ? "text-red-600 dark:text-red-400" : "text-blue-600 dark:text-blue-400";

    // Detect file edit tools with oldStr/newStr for diff rendering
    const input = part.input as Record<string, unknown> | undefined;
    const isFileEdit = input && typeof input === "object" && "oldStr" in input && "newStr" in input;
    const filePath = input && typeof input === "object" ? (input.path as string) || (input.file_path as string) || "" : "";

    if (!hasDetails) {
      return (
        <div className={cn("my-1.5 rounded-md border px-3 py-2", borderColor)}>
          <div className={cn("flex items-center gap-2 text-xs font-medium", textColor)}>
            <Wrench className="h-3.5 w-3.5" />
            <span>{statusIcon} {toolName || "tool"}</span>
          </div>
        </div>
      );
    }

    return (
      <details className={cn("group my-1.5 rounded-md border", borderColor)}>
        <summary className={cn("flex cursor-pointer items-center gap-2 px-3 py-2 text-xs font-medium", textColor)}>
          <Wrench className="h-3.5 w-3.5" />
          <span>{statusIcon} {toolName || "tool"}{filePath ? ` — ${filePath}` : ""}</span>
        </summary>
        <div className="border-t border-inherit py-1 space-y-1 overflow-hidden">
          {isFileEdit ? (
            <ToolDiffView oldStr={String(input.oldStr ?? "")} newStr={String(input.newStr ?? "")} />
          ) : (
            <>
              {part.input != null && (
                <pre className="overflow-x-auto text-xs text-muted-foreground whitespace-pre-wrap font-mono max-h-40 overflow-y-auto px-3">
                  {typeof part.input === "string" ? String(part.input) : JSON.stringify(part.input, null, 2)}
                </pre>
              )}
              {part.toolOutput && (
                <pre className="overflow-x-auto text-xs text-muted-foreground whitespace-pre-wrap font-mono max-h-40 overflow-y-auto border-t border-muted pt-1 mt-1 px-3">
                  {part.toolOutput}
                </pre>
              )}
            </>
          )}
        </div>
      </details>
    );
  }

  if (part.type === "tool_result" && (part.text || typeof part.text === "string")) {
    return (
      <div className="my-1.5 rounded-md border border-green-500/20 bg-green-500/5 px-3 py-2">
        <div className="flex items-center gap-2 text-xs font-medium text-green-600 dark:text-green-400">
          <Server className="h-3.5 w-3.5" />
          Tool result
        </div>
        <pre className="mt-1 overflow-x-auto text-xs text-muted-foreground whitespace-pre-wrap font-mono">
          {part.text ?? ""}
        </pre>
      </div>
    );
  }

  return null;
}
