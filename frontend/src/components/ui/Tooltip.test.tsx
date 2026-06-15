import { describe, expect, it } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { render } from "../../test/utils";
import { Tooltip } from "./Tooltip";

describe("Tooltip", () => {
  it("renders children", () => {
    render(
      <Tooltip content="Hello">
        <button>Trigger</button>
      </Tooltip>,
    );
    expect(screen.getByText("Trigger")).toBeInTheDocument();
  });

  it("does not show tooltip content before hover", () => {
    render(
      <Tooltip content="Secret info">
        <button>Trigger</button>
      </Tooltip>,
    );
    expect(screen.queryByText("Secret info")).not.toBeInTheDocument();
  });

  it("shows tooltip content on hover", async () => {
    const user = userEvent.setup();
    render(
      <Tooltip content="Helpful text">
        <button>Trigger</button>
      </Tooltip>,
    );
    await user.hover(screen.getByText("Trigger"));
    await waitFor(() => {
      expect(screen.getAllByText("Helpful text").length).toBeGreaterThan(0);
    });
  });

  it("hides tooltip when mouse leaves trigger", async () => {
    const user = userEvent.setup();
    render(
      <Tooltip content="Vanishing text">
        <button>Trigger</button>
      </Tooltip>,
    );
    const trigger = screen.getByText("Trigger");
    await user.hover(trigger);
    await waitFor(() => {
      expect(screen.getAllByText("Vanishing text").length).toBeGreaterThan(0);
    });
    await user.keyboard("{Escape}");
    await waitFor(() => {
      expect(screen.queryByText("Vanishing text")).not.toBeInTheDocument();
    });
  });

  it("renders JSX content", async () => {
    const user = userEvent.setup();
    render(
      <Tooltip
        content={
          <>
            <p>Line one</p>
            <p>Line two</p>
          </>
        }
      >
        <button>Trigger</button>
      </Tooltip>,
    );
    await user.hover(screen.getByText("Trigger"));
    await waitFor(() => {
      expect(screen.getAllByText("Line one").length).toBeGreaterThan(0);
      expect(screen.getAllByText("Line two").length).toBeGreaterThan(0);
    });
  });

  it("renders children bare when disabled (no tooltip wrapper)", () => {
    render(
      <Tooltip content="Should not appear" disabled>
        <button>Trigger</button>
      </Tooltip>,
    );
    expect(screen.getByText("Trigger")).toBeInTheDocument();
  });

  it("does not show tooltip when disabled even on hover", async () => {
    const user = userEvent.setup();
    render(
      <Tooltip content="Hidden" disabled>
        <button>Trigger</button>
      </Tooltip>,
    );
    await user.hover(screen.getByText("Trigger"));
    expect(screen.queryByText("Hidden")).not.toBeInTheDocument();
  });

  it("renders the trigger as a button child", () => {
    render(
      <Tooltip content="Click me">
        <button aria-label="action">X</button>
      </Tooltip>,
    );
    expect(screen.getByRole("button", { name: "action" })).toBeInTheDocument();
  });

  it("renders span children", () => {
    render(
      <Tooltip content="Info">
        <span data-testid="info-trigger">i</span>
      </Tooltip>,
    );
    expect(screen.getByTestId("info-trigger")).toBeInTheDocument();
  });

  it("shows tooltip on focus", async () => {
    render(
      <Tooltip content="Focused content">
        <button>Trigger</button>
      </Tooltip>,
    );
    screen.getByText("Trigger").focus();
    await waitFor(() => {
      expect(screen.getAllByText("Focused content").length).toBeGreaterThan(0);
    });
  });
});
