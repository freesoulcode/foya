import DOMPurify from "dompurify";

let nextDiagramID = 1;
let renderQueue: Promise<void> = Promise.resolve();

async function render(source: string, dark: boolean): Promise<string> {
  const { default: mermaid } = await import("mermaid");
  mermaid.initialize({
    startOnLoad: false,
    securityLevel: "strict",
    suppressErrorRendering: true,
    theme: dark ? "dark" : "neutral",
    maxTextSize: 50_000,
    flowchart: {
      htmlLabels: false,
    },
  });

  const id = `foya-mermaid-${nextDiagramID++}`;
  const { svg } = await mermaid.render(id, source);
  const sanitized = DOMPurify.sanitize(svg, {
    USE_PROFILES: { svg: true, svgFilters: true },
    ADD_TAGS: ["style"],
    ADD_ATTR: ["xmlns"],
  });
  if (!sanitized.trim()) {
    throw new Error("Mermaid returned an empty diagram");
  }
  return sanitized;
}

export function renderMermaid(source: string, dark: boolean): Promise<string> {
  const result = renderQueue.then(() => render(source, dark));
  renderQueue = result.then(
    () => undefined,
    () => undefined
  );
  return result;
}
