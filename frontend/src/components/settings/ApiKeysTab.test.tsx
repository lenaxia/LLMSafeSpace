import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import { render } from "../../test/utils";
import { ApiKeysTab } from "./ApiKeysTab";

describe("ApiKeysTab", () => {
  it("renders heading", () => {
    render(<ApiKeysTab />);
    expect(screen.getByText("API Keys")).toBeInTheDocument();
  });

  it("renders create button", () => {
    render(<ApiKeysTab />);
    expect(screen.getByRole("button", { name: /create key/i })).toBeInTheDocument();
  });

  it("renders empty state message", () => {
    render(<ApiKeysTab />);
    expect(screen.getByText(/no api keys yet/i)).toBeInTheDocument();
  });
});
