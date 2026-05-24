import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import { render } from "../../test/utils";
import { Badge } from "./Badge";

describe("Badge", () => {
  it("renders text content", () => {
    render(<Badge>Active</Badge>);
    expect(screen.getByText("Active")).toBeInTheDocument();
  });

  it("applies default variant styles", () => {
    render(<Badge data-testid="badge">Default</Badge>);
    expect(screen.getByTestId("badge").className).toContain("bg-primary/10");
  });

  it("applies success variant", () => {
    render(<Badge variant="success" data-testid="badge">OK</Badge>);
    expect(screen.getByTestId("badge").className).toContain("bg-green-500/10");
  });

  it("applies destructive variant", () => {
    render(<Badge variant="destructive" data-testid="badge">Error</Badge>);
    expect(screen.getByTestId("badge").className).toContain("bg-destructive/10");
  });

  it("applies muted variant", () => {
    render(<Badge variant="muted" data-testid="badge">Muted</Badge>);
    expect(screen.getByTestId("badge").className).toContain("bg-muted");
  });
});
