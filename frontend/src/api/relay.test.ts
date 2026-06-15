import { describe, it, expect, vi, beforeEach } from "vitest";
import { relayApi } from "./relay";
import { api } from "./client";

vi.mock("./client", () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
  },
}));

describe("relayApi", () => {
  beforeEach(() => vi.clearAllMocks());

  describe("getSetup", () => {
    it("calls GET /admin/relay/setup", async () => {
      vi.mocked(api.get).mockResolvedValue({ deployed: false });
      await relayApi.getSetup();
      expect(api.get).toHaveBeenCalledWith("/admin/relay/setup");
    });
  });

  describe("getStatus", () => {
    it("calls GET /admin/relay/status", async () => {
      vi.mocked(api.get).mockResolvedValue({ deployed: true });
      await relayApi.getStatus();
      expect(api.get).toHaveBeenCalledWith("/admin/relay/status");
    });
  });

  describe("saveOCICreds", () => {
    it("calls POST /admin/relay/oci-creds with credentials", async () => {
      vi.mocked(api.post).mockResolvedValue({ configured: true });
      await relayApi.saveOCICreds({
        tenancy: "t",
        user: "u",
        fingerprint: "f",
        key: "k",
        region: "us-ashburn-1",
      });
      expect(api.post).toHaveBeenCalledWith("/admin/relay/oci-creds", {
        tenancy: "t",
        user: "u",
        fingerprint: "f",
        key: "k",
        region: "us-ashburn-1",
      });
    });
  });

  describe("saveGCPCreds", () => {
    it("calls POST /admin/relay/gcp-creds with service account JSON", async () => {
      vi.mocked(api.post).mockResolvedValue({ configured: true });
      await relayApi.saveGCPCreds({ serviceAccountJson: '{"type":"service_account"}' });
      expect(api.post).toHaveBeenCalledWith("/admin/relay/gcp-creds", {
        serviceAccountJson: '{"type":"service_account"}',
      });
    });
  });

  describe("deploy", () => {
    it("calls POST /admin/relay/deploy with fleet config", async () => {
      vi.mocked(api.post).mockResolvedValue({ deployed: true });
      await relayApi.deploy({
        upstreamURL: "https://example.com",
        routerEndpoint: "gw:51820",
        providers: ["oci", "gcp"],
      });
      expect(api.post).toHaveBeenCalledWith("/admin/relay/deploy", {
        upstreamURL: "https://example.com",
        routerEndpoint: "gw:51820",
        providers: ["oci", "gcp"],
      });
    });
  });

  describe("rotate", () => {
    it("calls POST /admin/relay/rotate/:id", async () => {
      vi.mocked(api.post).mockResolvedValue({ rotating: "oci-1" });
      await relayApi.rotate("oci-1");
      expect(api.post).toHaveBeenCalledWith("/admin/relay/rotate/oci-1");
    });
  });

  describe("pause", () => {
    it("calls POST /admin/relay/pause", async () => {
      vi.mocked(api.post).mockResolvedValue({ paused: true });
      await relayApi.pause();
      expect(api.post).toHaveBeenCalledWith("/admin/relay/pause");
    });
  });

  describe("resume", () => {
    it("calls POST /admin/relay/resume", async () => {
      vi.mocked(api.post).mockResolvedValue({ paused: false });
      await relayApi.resume();
      expect(api.post).toHaveBeenCalledWith("/admin/relay/resume");
    });
  });
});
