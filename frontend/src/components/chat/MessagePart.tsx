import ReactMarkdown from "react-markdown";
import rehypeSanitize from "rehype-sanitize";
import { Brain, Wrench, Server } from "lucide-react";
import type { MessagePart as MessagePartType } from "../../api/types";
import { cn } from "../../lib/utils";

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
    return (
      <div className={cn("prose prose-sm dark:prose-invert max-w-none")}>
        <ReactMarkdown rehypePlugins={[rehypeSanitize]}>
          {part.text}
        </ReactMarkdown>
      </div>
    );
  }

  if ((part.type === "thinking" || part.type === "reasoning") && part.text) {
    if (isStreaming) {
      // While streaming: show inline with a left-border indicator (actively generating)
      return (
        <div className="my-2 border-l-2 border-muted-foreground/40 pl-3">
          <div className="mb-1 flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
            <Brain className="h-3.5 w-3.5 animate-pulse" />
            <span>Thinking…</span>
          </div>
          <div className="text-xs text-muted-foreground/80 italic whitespace-pre-wrap">
            {part.text}
          </div>
        </div>
      );
    }
    // Completed: collapsible with blockquote-style content
    return (
      <details className="group my-2 rounded-md border border-muted-foreground/20 bg-muted/30">
        <summary className="flex cursor-pointer items-center gap-2 px-3 py-1.5 text-xs font-medium text-muted-foreground hover:text-foreground">
          <Brain className="h-3.5 w-3.5" />
          Thinking
        </summary>
        <div className="border-t border-muted-foreground/10 px-3 py-2">
          <div className="border-l-2 border-muted-foreground/30 pl-3 text-xs text-muted-foreground italic">
            <ReactMarkdown rehypePlugins={[rehypeSanitize]}>
              {part.text}
            </ReactMarkdown>
          </div>
        </div>
      </details>
    );
  }

  if ((part.type === "tool_use" || part.type === "tool_call") && (part.text || part.name)) {
    const toolName = part.name ?? part.text?.split("(")[0] ?? "tool";
    const toolArgs = part.input ?? (part.text ? part.text.substring(part.text.indexOf("(")) : "");
    return (
      <div className="my-1.5 rounded-md border border-blue-500/20 bg-blue-500/5 px-3 py-2">
        <div className="flex items-center gap-2 text-xs font-medium text-blue-600 dark:text-blue-400">
          <Wrench className="h-3.5 w-3.5" />
          Tool call: {toolName}
        </div>
        <pre className="mt-1 overflow-x-auto text-xs text-muted-foreground whitespace-pre-wrap font-mono">
          {typeof toolArgs === "string" ? toolArgs : JSON.stringify(toolArgs, null, 2)}
        </pre>
      </div>
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
