import type { Message } from "../../api/types";
import { cn } from "../../lib/utils";
import { MessagePart } from "./MessagePart";

interface Props {
  message: Message;
}

export function MessageBubble({ message }: Props) {
  const isUser = message.role === "user";

  return (
    <div className={cn("flex w-full", isUser ? "justify-end" : "justify-start")}>
      <div
        className={cn(
          "max-w-[80%] rounded-lg px-4 py-2.5",
          isUser
            ? "bg-primary text-primary-foreground"
            : "bg-muted text-foreground",
        )}
      >
        {message.parts.map((part, i) => (
          <MessagePart key={i} part={part} isUser={isUser} />
        ))}
      </div>
    </div>
  );
}
