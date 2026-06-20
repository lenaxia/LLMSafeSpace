import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { render } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useNavigate } from "react-router-dom";
import { useCollapsibleSidebar } from "./useCollapsibleSidebar";

function setMobileMatchMedia(isMobile: boolean) {
  vi.spyOn(window, "matchMedia").mockImplementation((query) => {
    const isMinWidthQuery = query.includes("min-width");
    return {
      matches: isMinWidthQuery ? !isMobile : false,
      media: query,
      onchange: null,
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    } as unknown as MediaQueryList;
  });
}

function Harness({ onState }: { onState?: (s: ReturnType<typeof useCollapsibleSidebar>) => void }) {
  const sidebar = useCollapsibleSidebar();
  const navigate = useNavigate();
  onState?.(sidebar);
  return (
    <>
      <div data-testid="open">{String(sidebar.open)}</div>
      <div data-testid="is-mobile">{String(sidebar.isMobile)}</div>
      <div data-testid="width">{sidebar.sidebarWidth}</div>
      <button data-testid="open-btn" onClick={() => sidebar.setOpen(true)}>open</button>
      <button data-testid="close-btn" onClick={() => sidebar.close()}>close</button>
      <button data-testid="nav-btn" onClick={() => navigate("/other")}>nav</button>
    </>
  );
}

function renderHarness(initialPath = "/") {
  return render(
    <MemoryRouter initialEntries={[initialPath]}>
      <Routes>
        <Route path="/" element={<Harness />} />
        <Route path="/other" element={<Harness />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("useCollapsibleSidebar", () => {
  beforeEach(() => {
    setMobileMatchMedia(true);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("exposes open state starting closed", () => {
    const { getByTestId } = renderHarness();
    expect(getByTestId("open").textContent).toBe("false");
  });

  it("reports isMobile based on matchMedia", () => {
    const { getByTestId } = renderHarness();
    expect(getByTestId("is-mobile").textContent).toBe("true");
  });

  it("setOpen(true) opens the sidebar", async () => {
    const user = userEvent.setup();
    const { getByTestId } = renderHarness();
    await user.click(getByTestId("open-btn"));
    expect(getByTestId("open").textContent).toBe("true");
  });

  it("close() closes an open sidebar", async () => {
    const user = userEvent.setup();
    const { getByTestId } = renderHarness();
    await user.click(getByTestId("open-btn"));
    await user.click(getByTestId("close-btn"));
    expect(getByTestId("open").textContent).toBe("false");
  });

  it("closes the sidebar on route change", async () => {
    const user = userEvent.setup();
    const { getByTestId } = renderHarness();
    await user.click(getByTestId("open-btn"));
    expect(getByTestId("open").textContent).toBe("true");
    await user.click(getByTestId("nav-btn"));
    expect(getByTestId("open").textContent).toBe("false");
  });

  it("does not close on initial mount when route is the first entry", () => {
    const { getByTestId } = renderHarness("/other");
    expect(getByTestId("open").textContent).toBe("false");
  });

  it("defaults sidebarWidth to 256", () => {
    const { getByTestId } = renderHarness();
    expect(getByTestId("width").textContent).toBe("256");
  });

  it("honours a custom sidebarWidth", () => {
    function CustomWidthHarness() {
      const sidebar = useCollapsibleSidebar({ sidebarWidth: 192 });
      return <div data-testid="width">{sidebar.sidebarWidth}</div>;
    }
    const { getByTestId } = render(
      <MemoryRouter>
        <CustomWidthHarness />
      </MemoryRouter>,
    );
    expect(getByTestId("width").textContent).toBe("192");
  });

  it("exposes container, sidebar, and overlay refs", () => {
    let captured: ReturnType<typeof useCollapsibleSidebar> | null = null;
    render(
      <MemoryRouter>
        <Harness onState={(s) => { captured = s; }} />
      </MemoryRouter>,
    );
    expect(captured).not.toBeNull();
    expect(captured!.containerRef).toBeDefined();
    expect(captured!.sidebarRef).toBeDefined();
    expect(captured!.overlayRef).toBeDefined();
  });

  it("reports isMobile=false when matchMedia matches the desktop query", () => {
    setMobileMatchMedia(false);
    const { getByTestId } = renderHarness();
    expect(getByTestId("is-mobile").textContent).toBe("false");
  });
});
