import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { ReadOnlyBanner } from "./ReadOnlyBanner";

describe("ReadOnlyBanner", () => {
  it("renders the default message when none provided", () => {
    render(<ReadOnlyBanner />);
    expect(
      screen.getByText(/Subtasks are view-only/i),
    ).toBeInTheDocument();
  });

  it("renders a custom message when provided", () => {
    render(<ReadOnlyBanner message="Custom read-only reason" />);
    expect(screen.getByText("Custom read-only reason")).toBeInTheDocument();
  });

  it("is exposed as a status landmark for assistive tech", () => {
    render(<ReadOnlyBanner />);
    expect(screen.getByRole("status")).toBeInTheDocument();
  });

  it("does not render any interactive controls", () => {
    render(<ReadOnlyBanner />);
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });
});
