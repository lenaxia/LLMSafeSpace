import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import { SettingsForm } from "./SettingsForm";
import type { SettingDef } from "../../api/settings";

const mockSchema: SettingDef[] = [
  { key: "test.bool", tier: 3, type: "bool", default: false, category: "Test", label: "Bool Setting", description: "A boolean" },
  { key: "test.int", tier: 3, type: "int", default: 14, min: 10, max: 24, category: "Test", label: "Int Setting", description: "An integer" },
  { key: "test.enum", tier: 3, type: "enum", default: "a", enum: ["a", "b", "c"], category: "Test", label: "Enum Setting", description: "An enum" },
  { key: "test.string", tier: 3, type: "string", default: "", category: "Other", label: "String Setting", description: "A string" },
];

describe("SettingsForm", () => {
  it("renders all categories", () => {
    render(<SettingsForm schema={mockSchema} values={{}} onSave={vi.fn()} />);
    expect(screen.getByText("Test")).toBeInTheDocument();
    expect(screen.getByText("Other")).toBeInTheDocument();
  });

  it("renders labels and descriptions", () => {
    render(<SettingsForm schema={mockSchema} values={{}} onSave={vi.fn()} />);
    expect(screen.getByText("Bool Setting")).toBeInTheDocument();
    expect(screen.getByText("A boolean")).toBeInTheDocument();
    expect(screen.getByText("Int Setting")).toBeInTheDocument();
  });

  it("renders toggle for bool type", () => {
    render(<SettingsForm schema={mockSchema} values={{ "test.bool": true }} onSave={vi.fn()} />);
    const toggle = screen.getByRole("switch");
    expect(toggle).toBeInTheDocument();
    expect(toggle).toHaveAttribute("data-state", "checked");
  });

  it("renders number input for int type", () => {
    render(<SettingsForm schema={mockSchema} values={{ "test.int": 18 }} onSave={vi.fn()} />);
    const input = screen.getByRole("spinbutton");
    expect(input).toHaveValue(18);
  });

  it("renders text input for string type", () => {
    render(<SettingsForm schema={mockSchema} values={{ "test.string": "hello" }} onSave={vi.fn()} />);
    const input = screen.getByDisplayValue("hello");
    expect(input).toBeInTheDocument();
  });

  it("calls onSave when toggle is clicked", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<SettingsForm schema={mockSchema} values={{ "test.bool": false }} onSave={onSave} />);

    const toggle = screen.getByRole("switch");
    fireEvent.click(toggle);

    await waitFor(() => {
      expect(onSave).toHaveBeenCalledWith("test.bool", true);
    });
  });

  it("calls onSave when number input is changed and blurred", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<SettingsForm schema={mockSchema} values={{ "test.int": 14 }} onSave={onSave} />);

    const input = screen.getByRole("spinbutton");
    fireEvent.change(input, { target: { value: "20" } });
    fireEvent.blur(input);

    await waitFor(() => {
      expect(onSave).toHaveBeenCalledWith("test.int", 20);
    });
  });

  it("does not crash when onSave fails", async () => {
    const onSave = vi.fn().mockRejectedValue(new Error("Network error"));
    render(<SettingsForm schema={mockSchema} values={{ "test.bool": false }} onSave={onSave} />);

    const toggle = screen.getByRole("switch");
    fireEvent.click(toggle);

    await waitFor(() => {
      expect(onSave).toHaveBeenCalled();
    });
    // Error is handled by parent (toast) — no inline error shown
  });

  it("uses default value when value not in values map", () => {
    render(<SettingsForm schema={mockSchema} values={{}} onSave={vi.fn()} />);
    // Int default is 14
    const input = screen.getByRole("spinbutton");
    expect(input).toHaveValue(14);
  });

  it("disables controls when disabled prop is true", () => {
    render(<SettingsForm schema={mockSchema} values={{}} onSave={vi.fn()} disabled />);
    const toggle = screen.getByRole("switch");
    expect(toggle).toBeDisabled();
    const input = screen.getByRole("spinbutton");
    expect(input).toBeDisabled();
  });

  describe("responsive layout", () => {
    it("setting rows use responsive flex direction (column on mobile, row on desktop)", () => {
      const { container } = render(<SettingsForm schema={mockSchema} values={{}} onSave={vi.fn()} />);
      const rows = container.querySelectorAll(".divide-y > div");
      // Each row should have flex-col for mobile and sm:flex-row for desktop
      rows.forEach((row) => {
        expect(row.className).toContain("flex-col");
        expect(row.className).toContain("sm:flex-row");
      });
    });

    it("string input uses full width on mobile and fixed width on desktop", () => {
      render(<SettingsForm schema={mockSchema} values={{ "test.string": "hello" }} onSave={vi.fn()} />);
      const input = screen.getByDisplayValue("hello");
      expect(input.className).toContain("w-full");
      expect(input.className).toContain("sm:w-48");
    });
  });
});
