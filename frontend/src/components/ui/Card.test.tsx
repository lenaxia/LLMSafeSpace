import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import { render } from "../../test/utils";
import { Card, CardHeader, CardTitle, CardDescription, CardContent } from "./Card";

describe("Card", () => {
  it("renders children", () => {
    render(<Card>Card content</Card>);
    expect(screen.getByText("Card content")).toBeInTheDocument();
  });

  it("applies border and background styles", () => {
    render(<Card data-testid="card">Content</Card>);
    const el = screen.getByTestId("card");
    expect(el.className).toContain("border");
    expect(el.className).toContain("bg-card");
  });

  it("renders full card composition", () => {
    render(
      <Card>
        <CardHeader>
          <CardTitle>Title</CardTitle>
          <CardDescription>Description</CardDescription>
        </CardHeader>
        <CardContent>Body</CardContent>
      </Card>,
    );
    expect(screen.getByText("Title")).toBeInTheDocument();
    expect(screen.getByText("Description")).toBeInTheDocument();
    expect(screen.getByText("Body")).toBeInTheDocument();
  });

  it("CardTitle renders as h3", () => {
    render(<CardTitle>Heading</CardTitle>);
    expect(screen.getByRole("heading", { level: 3 })).toHaveTextContent("Heading");
  });
});
