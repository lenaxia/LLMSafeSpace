import "@testing-library/jest-dom/vitest";

// jsdom doesn't implement scrollIntoView
Element.prototype.scrollIntoView = () => {};

// jsdom doesn't implement matchMedia
Object.defineProperty(window, "matchMedia", {
  writable: true,
  value: (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  }),
});

// jsdom doesn't implement ResizeObserver (needed by @tanstack/react-virtual)
class MockResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}
Object.defineProperty(window, "ResizeObserver", {
  writable: true,
  value: MockResizeObserver,
});

// Give elements a non-zero size for virtualizer to work
Object.defineProperty(HTMLElement.prototype, "offsetHeight", { configurable: true, get() { return 500; } });
Object.defineProperty(HTMLElement.prototype, "offsetWidth", { configurable: true, get() { return 800; } });

// Mock getBoundingClientRect for scroll containers
Element.prototype.getBoundingClientRect = function () {
  return { top: 0, left: 0, bottom: 500, right: 800, width: 800, height: 500, x: 0, y: 0, toJSON: () => {} };
};
