import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import { render } from "../../test/utils";
import { Spinner } from "./Spinner";

describe("Spinner", () => {
  it("renders with accessible label", () => {
    render(<Spinner />);
    expect(screen.getByLabelText("Loading")).toBeInTheDocument();
  });

  it("renders medium size by default", () => {
    render(<Spinner />);
    const svg = screen.getByLabelText("Loading");
    expect(svg.getAttribute("class")).toContain("h-6");
    expect(svg.getAttribute("class")).toContain("w-6");
  });

  it("renders small size", () => {
    render(<Spinner size="sm" />);
    const svg = screen.getByLabelText("Loading");
    expect(svg.getAttribute("class")).toContain("h-4");
  });

  it("renders large size", () => {
    render(<Spinner size="lg" />);
    const svg = screen.getByLabelText("Loading");
    expect(svg.getAttribute("class")).toContain("h-8");
  });

  it("has animation class", () => {
    render(<Spinner />);
    expect(screen.getByLabelText("Loading").getAttribute("class")).toContain("animate-spin");
  });
});
