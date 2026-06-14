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

  describe("saveAWSConfig", () => {
    it("calls POST /admin/relay/aws-config with config", async () => {
      vi.mocked(api.post).mockResolvedValue({ configured: true });
      await relayApi.saveAWSConfig({
        trustAnchorId: "ta-1",
        profileId: "p-1",
        roleArn: "arn:aws:iam::123:role/r",
        region: "us-east-1",
      });
      expect(api.post).toHaveBeenCalledWith("/admin/relay/aws-config", {
        trustAnchorId: "ta-1",
        profileId: "p-1",
        roleArn: "arn:aws:iam::123:role/r",
        region: "us-east-1",
      });
    });
  });

  describe("testAWS", () => {
    it("calls POST /admin/relay/test-aws", async () => {
      vi.mocked(api.post).mockResolvedValue({ valid: true, accountId: "123" });
      await relayApi.testAWS();
      expect(api.post).toHaveBeenCalledWith("/admin/relay/test-aws");
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

  describe("deploy", () => {
    it("calls POST /admin/relay/deploy with fleet config", async () => {
      vi.mocked(api.post).mockResolvedValue({ deployed: true });
      await relayApi.deploy({
        upstreamURL: "https://example.com",
        routerEndpoint: "gw:51820",
        providers: ["aws", "oci"],
      });
      expect(api.post).toHaveBeenCalledWith("/admin/relay/deploy", {
        upstreamURL: "https://example.com",
        routerEndpoint: "gw:51820",
        providers: ["aws", "oci"],
      });
    });
  });

  describe("rotate", () => {
    it("calls POST /admin/relay/rotate/:id", async () => {
      vi.mocked(api.post).mockResolvedValue({ rotating: "aws-1" });
      await relayApi.rotate("aws-1");
      expect(api.post).toHaveBeenCalledWith("/admin/relay/rotate/aws-1");
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
