import { describe, expect, it } from "vitest";
import { render } from "../../test/utils";
import { StreamingIndicator } from "./StreamingIndicator";

describe("StreamingIndicator", () => {
  it("renders three animated dots", () => {
    const { container } = render(<StreamingIndicator />);
    const dots = container.querySelectorAll("span");
    expect(dots).toHaveLength(3);
  });

  it("dots have bounce animation", () => {
    const { container } = render(<StreamingIndicator />);
    const dots = container.querySelectorAll("span");
    dots.forEach((dot) => {
      expect(dot.className).toContain("animate-bounce");
    });
  });
});
