import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import { render } from "../../test/utils";
import { AuthCard } from "./AuthCard";

describe("AuthCard", () => {
  it("renders title and description", () => {
    render(
      <AuthCard title="Welcome" description="Sign in to continue">
        <div>form</div>
      </AuthCard>,
    );
    expect(screen.getByText("Welcome")).toBeInTheDocument();
    expect(screen.getByText("Sign in to continue")).toBeInTheDocument();
  });

  it("renders children", () => {
    render(
      <AuthCard title="T" description="D">
        <button>Submit</button>
      </AuthCard>,
    );
    expect(screen.getByRole("button", { name: "Submit" })).toBeInTheDocument();
  });

  it("renders footer when provided", () => {
    render(
      <AuthCard title="T" description="D" footer={<a href="/register">Register</a>}>
        <div>form</div>
      </AuthCard>,
    );
    expect(screen.getByRole("link", { name: "Register" })).toBeInTheDocument();
  });

  it("does not render footer when not provided", () => {
    render(
      <AuthCard title="T" description="D">
        <div>form</div>
      </AuthCard>,
    );
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
  });
});
