import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import { render } from "../../test/utils";
import { ComingSoonTab } from "./ComingSoonTab";

describe("ComingSoonTab", () => {
  it("renders the tab name", () => {
    render(<ComingSoonTab name="Profile" />);
    expect(screen.getByText("Profile")).toBeInTheDocument();
  });

  it("renders coming soon text", () => {
    render(<ComingSoonTab name="MCP Servers" />);
    expect(screen.getByText("Coming soon")).toBeInTheDocument();
  });
});
