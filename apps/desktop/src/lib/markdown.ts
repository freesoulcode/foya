import { Marked, type Tokens } from "marked";
import DOMPurify from "dompurify";
import hljs from "highlight.js/lib/core";

import javascript from "highlight.js/lib/languages/javascript";
import typescript from "highlight.js/lib/languages/typescript";
import python from "highlight.js/lib/languages/python";
import go from "highlight.js/lib/languages/go";
import rust from "highlight.js/lib/languages/rust";
import java from "highlight.js/lib/languages/java";
import c from "highlight.js/lib/languages/c";
import cpp from "highlight.js/lib/languages/cpp";
import csharp from "highlight.js/lib/languages/csharp";
import php from "highlight.js/lib/languages/php";
import ruby from "highlight.js/lib/languages/ruby";
import swift from "highlight.js/lib/languages/swift";
import kotlin from "highlight.js/lib/languages/kotlin";
import dart from "highlight.js/lib/languages/dart";
import bash from "highlight.js/lib/languages/bash";
import shell from "highlight.js/lib/languages/shell";
import sql from "highlight.js/lib/languages/sql";
import json from "highlight.js/lib/languages/json";
import yaml from "highlight.js/lib/languages/yaml";
import xml from "highlight.js/lib/languages/xml";
import css from "highlight.js/lib/languages/css";
import markdown from "highlight.js/lib/languages/markdown";
import dockerfile from "highlight.js/lib/languages/dockerfile";
import diff from "highlight.js/lib/languages/diff";
import plaintext from "highlight.js/lib/languages/plaintext";

[
  javascript,
  typescript,
  python,
  go,
  rust,
  java,
  c,
  cpp,
  csharp,
  php,
  ruby,
  swift,
  kotlin,
  dart,
  bash,
  shell,
  sql,
  json,
  yaml,
  xml,
  css,
  markdown,
  dockerfile,
  diff,
  plaintext,
].forEach((lang) => hljs.registerLanguage(lang.name, lang));

export function highlightCode(code: string, lang?: string): string {
  if (lang && hljs.getLanguage(lang)) {
    try {
      return hljs.highlight(code, { language: lang, ignoreIllegals: true }).value;
    } catch {
      // fall through to auto
    }
  }
  return escapeHtml(code);
}

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

const marked = new Marked({ gfm: true, breaks: true });

marked.use({
  tokenizer: {
    // Marked 的 GFM 规则接受单个 ~ 作为删除线分隔符，会误伤
    // “4~5 级转 3~4 级”这类范围文本。这里只保留标准的 ~~...~~。
    del(src: string): Tokens.Del | undefined {
      const match =
        /^(~~)(?=[^\s~])((?:\\[\s\S]|[^\\])*?(?:\\[\s\S]|[^\s~\\]))\1(?=[^~]|$)/.exec(
          src
        );
      if (!match) return undefined;
      return {
        type: "del",
        raw: match[0],
        text: match[2],
        tokens: this.lexer.inlineTokens(match[2]),
      };
    },
  },
  renderer: {
    code(token: Tokens.Code): string {
      const lang = (token.lang || "").split(/\s+/)[0];
      const highlighted = highlightCode(token.text, lang);
      const langClass = lang ? ` language-${escapeHtml(lang)}` : "";
      const langLabel = lang ? escapeHtml(lang) : "text";
      return (
        `<div class="code-block">` +
        `<div class="code-block-header">` +
        `<span class="code-block-lang">${langLabel}</span>` +
        `<button type="button" class="code-block-copy" title="复制代码">复制</button>` +
        `</div>` +
        `<pre><code class="hljs${langClass}">${highlighted}</code></pre>` +
        `</div>`
      );
    },
    table(token: Tokens.Table): string {
      const headerCells = token.header
        .map((cell) => `<th>${this.parser.parseInline(cell.tokens)}</th>`)
        .join("");
      const bodyRows = token.rows
        .map(
          (row) =>
            `<tr>${row
              .map((cell) => `<td>${this.parser.parseInline(cell.tokens)}</td>`)
              .join("")}</tr>`
        )
        .join("");
      return (
        `<div class="table-wrapper">` +
        `<table>` +
        `<thead><tr>${headerCells}</tr></thead>` +
        `<tbody>${bodyRows}</tbody>` +
        `</table>` +
        `</div>`
      );
    },
  },
});

export function renderMarkdown(src: string): string {
  const html = marked.parse(src, { async: false }) as string;
  return DOMPurify.sanitize(html, { USE_PROFILES: { html: true } });
}
