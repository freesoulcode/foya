import { describe, expect, it } from "vitest";
import type { AttachmentRef } from "@/lib/api";
import {
  artifactMediaType,
  isImageArtifact,
  isVideoArtifact,
} from "./artifactMedia";

function attachment(name: string, mediaType = "application/octet-stream"): AttachmentRef {
  return {
    id: "artifact-1",
    name,
    kind: "file",
    media_type: mediaType,
    bytes: 1,
  };
}

describe("artifact media detection", () => {
  it("recognizes file artifacts by image extension", () => {
    const artifact = attachment("preview.PNG");
    expect(artifactMediaType(artifact)).toBe("image/png");
    expect(isImageArtifact(artifact)).toBe(true);
  });

  it("recognizes file artifacts by video extension", () => {
    const artifact = attachment("render.m4v");
    expect(artifactMediaType(artifact)).toBe("video/mp4");
    expect(isVideoArtifact(artifact)).toBe(true);
  });

  it("prefers a media extension over an incorrect generic type", () => {
    const artifact = attachment("clip.webm", "text/plain");
    expect(artifactMediaType(artifact)).toBe("video/webm");
    expect(isVideoArtifact(artifact)).toBe(true);
  });

  it("preserves a specific declared media type", () => {
    const artifact = attachment("download.bin", "video/mp4; charset=binary");
    expect(artifactMediaType(artifact)).toBe("video/mp4");
    expect(isVideoArtifact(artifact)).toBe(true);
  });
});
