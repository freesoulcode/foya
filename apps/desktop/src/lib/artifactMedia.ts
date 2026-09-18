import type { AttachmentRef } from "@/lib/api";

const MEDIA_TYPES_BY_EXTENSION: Record<string, string> = {
  avif: "image/avif",
  bmp: "image/bmp",
  gif: "image/gif",
  ico: "image/x-icon",
  jpeg: "image/jpeg",
  jpg: "image/jpeg",
  png: "image/png",
  svg: "image/svg+xml",
  webp: "image/webp",
  avi: "video/x-msvideo",
  m4v: "video/mp4",
  mkv: "video/x-matroska",
  mov: "video/quicktime",
  mp4: "video/mp4",
  ogg: "video/ogg",
  ogv: "video/ogg",
  webm: "video/webm",
};

function extension(name?: string): string {
  const filename = name?.trim().toLowerCase() ?? "";
  const dot = filename.lastIndexOf(".");
  return dot >= 0 ? filename.slice(dot + 1) : "";
}

function normalizedMediaType(mediaType?: string): string {
  return (mediaType || "")
    .split(";")[0]
    .trim()
    .toLowerCase();
}

export function mediaTypeForFileName(name?: string, mediaType?: string): string {
  const declared = normalizedMediaType(mediaType);
  const inferred = MEDIA_TYPES_BY_EXTENSION[extension(name)] ?? "";
  if (
    inferred &&
    (
      !declared ||
      declared === "application/octet-stream" ||
      (inferred.startsWith("image/") && !declared.startsWith("image/")) ||
      (inferred.startsWith("video/") && !declared.startsWith("video/"))
    )
  ) {
    return inferred;
  }
  return declared || inferred || "application/octet-stream";
}

export function artifactMediaType(attachment?: AttachmentRef): string {
  return mediaTypeForFileName(attachment?.name, attachment?.media_type);
}

export function isImageFileName(name?: string): boolean {
  return mediaTypeForFileName(name).startsWith("image/");
}

export function isVideoFileName(name?: string): boolean {
  return mediaTypeForFileName(name).startsWith("video/");
}

export function isImageArtifact(attachment?: AttachmentRef): boolean {
  return attachment?.kind === "image" || artifactMediaType(attachment).startsWith("image/");
}

export function isVideoArtifact(attachment?: AttachmentRef): boolean {
  return attachment?.kind === "video" || artifactMediaType(attachment).startsWith("video/");
}
