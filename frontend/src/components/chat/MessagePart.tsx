import { lazy, Suspense } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import rehypeSanitize from "rehype-sanitize";
import { Brain, Wrench, Server } from "lucide-react";
import { cn } from "../../lib/utils";
import { useUserSetting } from "../../hooks/useUserSettings";
import type { MessagePart as MessagePartType } from "../../api/types";

const ReactDiffViewer = lazy(() => import("react-diff-viewer-continued"));

function ToolInput({ input }: { input: unknown }) {
  if (!input || typeof input !== "object") {
    return <pre className="text-xs text-muted-foreground font-mono">{String(input)}</pre>;
  }
  const obj = input as Record<string, unknown>;
  // For bash/shell: show command inline
  if ("command" in obj && typeof obj.command === "string") {
    return <code className="block text-xs font-mono text-muted-foreground bg-muted/50 rounded px-2 py-1 whitespace-pre-wrap">$ {obj.command}</code>;
  }
  // For webfetch/read: show URL or path
  if ("url" in obj && typeof obj.url === "string") {
    return <code className="block text-xs font-mono text-muted-foreground truncate">{obj.url}</code>;
  }
  if ("path" in obj && typeof obj.path === "string" && Object.keys(obj).length <= 2) {
    return <code className="block text-xs font-mono text-muted-foreground truncate">{obj.path}</code>;
  }
  // Fallback: compact JSON
  return <pre className="text-xs text-muted-foreground font-mono whitespace-pre-wrap max-h-20 overflow-y-auto">{JSON.stringify(input, null, 2)}</pre>;
}

import { LazyDetails } from "../ui/LazyDetails";

function ToolDetails({ borderColor, textColor, statusIcon, toolName, filePath, children }: {
  borderColor: string; textColor: string; statusIcon: string; toolName: string; filePath: string; children: React.ReactNode;
}) {
  return (
    <LazyDetails
      className={cn("my-1.5 rounded-md border", borderColor)}
      contentClassName="border-t border-inherit py-1 space-y-1 min-w-0 overflow-hidden"
      summary={
        <summary className={cn("flex cursor-pointer items-center gap-2 px-3 py-2 text-xs font-medium overflow-hidden", textColor)}>
          <Wrench className="h-3.5 w-3.5 flex-shrink-0" />
          <span className="truncate">{statusIcon} {toolName || "tool"}{filePath ? ` — ${filePath}` : ""}</span>
        </summary>
      }
    >
      {children}
    </LazyDetails>
  );
}

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
            gutter: { minWidth: "20px", padding: "0 4px" },
            lineNumber: { fontSize: "9px" },
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
  const wordWrap = useUserSetting("codeBlockWordWrap", false);

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
      <div className={cn(
        "prose prose-sm dark:prose-invert max-w-none",
        "[&_pre]:overflow-x-auto [&_pre]:touch-pan-x [&_table]:block [&_table]:overflow-x-auto [&_table]:touch-pan-x [&_:not(pre)>code]:break-all",
        wordWrap && "[&_pre]:whitespace-pre-wrap [&_pre]:break-words",
      )}>
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

    // Detect file edit tools: opencode uses oldString/newString, others may use oldStr/newStr
    const input = part.input as Record<string, unknown> | undefined;
    const isFileEdit = input && typeof input === "object" && (
      ("oldString" in input && "newString" in input) || ("oldStr" in input && "newStr" in input)
    );
    const isFileWrite = !isFileEdit && input && typeof input === "object" && "content" in input && "filePath" in input;
    const filePath = input && typeof input === "object" ? (input.filePath as string) || (input.path as string) || (input.file_path as string) || "" : "";

    if (!hasDetails) {
      return (
        <div className={cn("my-1.5 rounded-md border px-3 py-2", borderColor)}>
          <div className={cn("flex items-center gap-2 text-xs font-medium overflow-hidden", textColor)}>
            <Wrench className="h-3.5 w-3.5 flex-shrink-0" />
            <span className="truncate">{statusIcon} {toolName || "tool"}</span>
          </div>
        </div>
      );
    }

    return (
      <ToolDetails borderColor={borderColor} textColor={textColor} statusIcon={statusIcon} toolName={toolName} filePath={filePath}>
        {isFileEdit ? (
          <ToolDiffView
            oldStr={String((input as Record<string, unknown>).oldString ?? (input as Record<string, unknown>).oldStr ?? "")}
            newStr={String((input as Record<string, unknown>).newString ?? (input as Record<string, unknown>).newStr ?? "")}
          />
        ) : isFileWrite ? (
          <pre className="overflow-x-auto touch-pan-x text-xs text-muted-foreground whitespace-pre-wrap font-mono max-h-60 overflow-y-auto px-3 py-1">
            {String((input as Record<string, unknown>).content ?? "")}
          </pre>
        ) : (
          <>
            {part.input != null && (
              <div className="px-3 py-1">
                <ToolInput input={part.input} />
              </div>
            )}
            {part.toolOutput && (
              <details className="border-t border-muted">
                <summary className="px-3 py-1 text-xs text-muted-foreground cursor-pointer hover:text-foreground">
                  Output ({part.toolOutput.length > 200 ? `${Math.ceil(part.toolOutput.length / 1024)}KB` : `${part.toolOutput.length} chars`})
                </summary>
                <pre className="overflow-x-auto touch-pan-x text-xs text-muted-foreground whitespace-pre-wrap font-mono max-h-60 overflow-y-auto px-3 py-1">
                  {part.toolOutput}
                </pre>
              </details>
            )}
          </>
        )}
      </ToolDetails>
    );
  }

  if (part.type === "tool_result" && (part.text || typeof part.text === "string")) {
    return (
      <div className="my-1.5 rounded-md border border-green-500/20 bg-green-500/5 px-3 py-2">
        <div className="flex items-center gap-2 text-xs font-medium text-green-600 dark:text-green-400">
          <Server className="h-3.5 w-3.5" />
          Tool result
        </div>
        <pre className="mt-1 overflow-x-auto touch-pan-x text-xs text-muted-foreground whitespace-pre-wrap font-mono">
          {part.text ?? ""}
        </pre>
      </div>
    );
  }

  if (part.type === "error" && part.text) {
    return (
      <div className="my-1.5 rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2">
        <p className="text-sm text-destructive">{part.text}</p>
      </div>
    );
  }

  return null;
}
