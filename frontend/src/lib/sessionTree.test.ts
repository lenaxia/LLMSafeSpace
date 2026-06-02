import { describe, it, expect } from "vitest";
import { buildSessionTree, ancestorChain } from "./sessionTree";
import type { SessionListItem } from "../api/types";

function s(id: string, parentId?: string): SessionListItem {
  return { id, parentId, messageCount: 0, status: "idle" };
}

describe("buildSessionTree", () => {
  it("treats sessions with no parentId as roots", () => {
    const tree = buildSessionTree([s("a"), s("b"), s("c")]);
    expect(tree.roots.map((n) => n.session.id)).toEqual(["a", "b", "c"]);
    expect(tree.orphans).toEqual([]);
    tree.roots.forEach((n) => expect(n.children).toEqual([]));
  });

  it("nests children under their parent", () => {
    const tree = buildSessionTree([
      s("root"),
      s("child1", "root"),
      s("child2", "root"),
    ]);
    expect(tree.roots).toHaveLength(1);
    expect(tree.roots[0]!.session.id).toBe("root");
    expect(tree.roots[0]!.children.map((n) => n.session.id)).toEqual(["child1", "child2"]);
  });

  it("nests grandchildren two levels deep", () => {
    const tree = buildSessionTree([
      s("root"),
      s("child", "root"),
      s("grandchild", "child"),
    ]);
    const root = tree.roots[0]!;
    expect(root.session.id).toBe("root");
    expect(root.children[0]!.session.id).toBe("child");
    expect(root.children[0]!.children[0]!.session.id).toBe("grandchild");
  });

  it("collects sessions with missing parent under orphans", () => {
    // 'orphan' references 'gone' which is not in the input list.
    const tree = buildSessionTree([s("a"), s("orphan", "gone")]);
    expect(tree.roots).toHaveLength(1);
    expect(tree.roots[0]!.session.id).toBe("a");
    expect(tree.orphans).toHaveLength(1);
    expect(tree.orphans[0]!.session.id).toBe("orphan");
  });

  it("orphans bring their own subtree along", () => {
    // 'orphan' references 'gone' (missing). 'orphan' itself has a child
    // 'orphanchild' which we still want to render as a child of orphan.
    const tree = buildSessionTree([
      s("root"),
      s("orphan", "gone"),
      s("orphanchild", "orphan"),
    ]);
    expect(tree.orphans).toHaveLength(1);
    expect(tree.orphans[0]!.session.id).toBe("orphan");
    expect(tree.orphans[0]!.children.map((n) => n.session.id)).toEqual(["orphanchild"]);
  });

  it("preserves input order for roots and children", () => {
    const tree = buildSessionTree([
      s("z"),
      s("a"),
      s("z-child2", "z"),
      s("z-child1", "z"),
    ]);
    expect(tree.roots.map((n) => n.session.id)).toEqual(["z", "a"]);
    expect(tree.roots[0]!.children.map((n) => n.session.id)).toEqual(["z-child2", "z-child1"]);
  });

  it("does not crash on a cyclic parent chain", () => {
    // a -> b -> a forms a cycle. Both end up as orphans (each parent
    // exists in the map but neither is reachable from a true root). The
    // tree should still build without infinite recursion.
    const tree = buildSessionTree([s("a", "b"), s("b", "a")]);
    expect(tree.roots).toEqual([]);
    // Both 'a' and 'b' have parents that ARE in the map, so they would
    // be classified as children of each other. The childrenByParent map
    // captures this. Neither becomes a "root" → no rendering, but no
    // crash. Cycle truncation in buildNode prevents the infinite loop
    // that would otherwise occur if we tried to recurse through them.
    // The acceptance criterion here is simply: terminates and produces
    // a finite result.
    expect(tree.orphans).toEqual([]);
  });

  it("returns empty when input is empty", () => {
    const tree = buildSessionTree([]);
    expect(tree.roots).toEqual([]);
    expect(tree.orphans).toEqual([]);
  });
});

describe("ancestorChain", () => {
  it("returns [self] for a top-level session", () => {
    const sessions = [s("root")];
    expect(ancestorChain(sessions, "root")).toEqual(["root"]);
  });

  it("returns [root, ...self] for a child", () => {
    const sessions = [s("root"), s("child", "root")];
    expect(ancestorChain(sessions, "child")).toEqual(["root", "child"]);
  });

  it("returns full chain for a grandchild", () => {
    const sessions = [
      s("root"),
      s("child", "root"),
      s("grandchild", "child"),
    ];
    expect(ancestorChain(sessions, "grandchild")).toEqual([
      "root",
      "child",
      "grandchild",
    ]);
  });

  it("stops at a missing parent (does not throw)", () => {
    // 'orphan' references 'gone' (missing); chain returns just [orphan].
    const sessions = [s("orphan", "gone")];
    expect(ancestorChain(sessions, "orphan")).toEqual(["orphan"]);
  });

  it("returns [] for a session not in the input", () => {
    expect(ancestorChain([s("a")], "missing")).toEqual([]);
  });

  it("does not loop forever on a cyclic chain", () => {
    const sessions = [s("a", "b"), s("b", "a")];
    const chain = ancestorChain(sessions, "a");
    // We don't care about the exact result — only that it terminates.
    expect(chain.length).toBeLessThanOrEqual(16);
  });
});
