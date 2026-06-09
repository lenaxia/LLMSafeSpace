import { describe, expect, it } from "vitest";
import { screen, fireEvent } from "@testing-library/react";
import { render } from "../../test/utils";
import { DiskUsageBar } from "./DiskUsageBar";

// DiskUsageBar renders both a desktop and mobile layout simultaneously.
// JSDOM does not apply CSS breakpoints, so both the `hidden sm:flex` (desktop)
// and `sm:hidden` (mobile) containers are visible. Use getAllBy* when a label
// or value is expected to appear in both layouts.

describe("DiskUsageBar — context unknown (contextTotal=0)", () => {
  // ── Happy path — known limit ─────────────────────────────────────────────

  it("renders context progress bar when contextTotal > 0", () => {
    render(<DiskUsageBar contextUsed={50000} contextTotal={200000} />);
    expect(screen.getAllByText(/context/i).length).toBeGreaterThan(0);
    expect(screen.getAllByText(/50K/).length).toBeGreaterThan(0);
    expect(screen.queryByText(/unknown/i)).not.toBeInTheDocument();
  });

  it("renders nothing when no metrics are provided", () => {
    const { container } = render(<DiskUsageBar />);
    expect(container.firstChild).toBeNull();
  });

  // ── Unknown context limit ────────────────────────────────────────────────

  it("shows 'Unknown' badge instead of progress bar when contextTotal=0", () => {
    render(<DiskUsageBar contextUsed={150000} contextTotal={0} />);
    expect(screen.getAllByText(/unknown/i).length).toBeGreaterThan(0);
  });

  it("shows 'Unknown' badge when contextTotal is undefined", () => {
    render(<DiskUsageBar contextUsed={150000} />);
    expect(screen.getAllByText(/unknown/i).length).toBeGreaterThan(0);
  });

  it("shows used token count even when limit is unknown", () => {
    render(<DiskUsageBar contextUsed={150000} contextTotal={0} />);
    expect(screen.getAllByText(/150K/).length).toBeGreaterThan(0);
  });

  it("does not render a progress bar when contextTotal=0", () => {
    const { container } = render(<DiskUsageBar contextUsed={150000} contextTotal={0} />);
    const progressBars = container.querySelectorAll(".bg-primary\\/40, .bg-orange-500");
    expect(progressBars).toHaveLength(0);
  });

  it("does not show a percentage when contextTotal=0", () => {
    render(<DiskUsageBar contextUsed={150000} contextTotal={0} />);
    expect(screen.queryByText(/%/)).not.toBeInTheDocument();
  });

  // ── Tooltip on unknown ───────────────────────────────────────────────────

  it("shows tooltip on hover of Unknown badge explaining auto-compaction is disabled", () => {
    render(<DiskUsageBar contextUsed={150000} contextTotal={0} />);
    const badge = screen.getAllByRole("button", { name: /context limit unknown/i })[0]!;
    fireEvent.mouseEnter(badge);
    expect(screen.getByText(/auto-compaction is disabled/i)).toBeInTheDocument();
  });

  it("hides tooltip when mouse leaves Unknown badge", () => {
    render(<DiskUsageBar contextUsed={150000} contextTotal={0} />);
    const badge = screen.getAllByRole("button", { name: /context limit unknown/i })[0]!;
    fireEvent.mouseEnter(badge);
    expect(screen.getByText(/auto-compaction is disabled/i)).toBeInTheDocument();
    fireEvent.mouseLeave(badge);
    expect(screen.queryByText(/auto-compaction is disabled/i)).not.toBeInTheDocument();
  });

  it("toggles tooltip on click of Unknown badge", () => {
    render(<DiskUsageBar contextUsed={150000} contextTotal={0} />);
    const badge = screen.getAllByRole("button", { name: /context limit unknown/i })[0]!;
    fireEvent.click(badge);
    expect(screen.getByText(/auto-compaction is disabled/i)).toBeInTheDocument();
    fireEvent.click(badge);
    expect(screen.queryByText(/auto-compaction is disabled/i)).not.toBeInTheDocument();
  });

  it("tooltip mentions max_input_tokens as the fix", () => {
    render(<DiskUsageBar contextUsed={150000} contextTotal={0} />);
    const badge = screen.getAllByRole("button", { name: /context limit unknown/i })[0]!;
    fireEvent.mouseEnter(badge);
    expect(screen.getByText(/max_input_tokens/i)).toBeInTheDocument();
  });

  // ── Edge cases ────────────────────────────────────────────────────────────

  it("still shows disk and memory metrics alongside unknown context", () => {
    render(<DiskUsageBar
      contextUsed={150000}
      contextTotal={0}
      diskUsedBytes={1024 * 1024 * 500}
      diskTotalBytes={1024 * 1024 * 1024 * 10}
    />);
    expect(screen.getAllByText(/unknown/i).length).toBeGreaterThan(0);
    expect(screen.getAllByText(/disk/i).length).toBeGreaterThan(0);
  });

  it("does not count unknown context as a critical metric", () => {
    render(<DiskUsageBar contextUsed={999999} contextTotal={0} />);
    expect(screen.queryByText(/critical/i)).not.toBeInTheDocument();
  });
});
