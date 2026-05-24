import ReactMarkdown from "react-markdown";
import rehypeSanitize from "rehype-sanitize";
import type { MessagePart as MessagePartType } from "../../api/types";
import { cn } from "../../lib/utils";

interface Props {
  part: MessagePartType;
  isUser: boolean;
}

export function MessagePart({ part, isUser }: Props) {
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
  return null;
}
