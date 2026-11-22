import { Fragment, type ReactNode } from "react";
import { Frame, Ctx } from "../header";
import { bound, connHead, label, str, type ShapeProps } from "./common";

// kind "text": notes and readouts. bind: title?, body (plain text or the
// small safe HTML subset p, br, b, i, ul, ol, li).
// The HTML is parsed into an inert document and rebuilt as React elements
// from a whitelist: no attributes, no other tags (their text is kept), no
// raw HTML ever reaches the page.

const ALLOWED: Record<string, string> = { p: "p", br: "br", b: "b", strong: "b", i: "i", em: "i", ul: "ul", ol: "ol", li: "li" };
const DROP = new Set(["script", "style", "template", "iframe", "object", "embed", "noscript", "svg", "math", "head", "title"]);
const MAX_NODES = 2000;

export function safeHtml(html: string): ReactNode[] {
  // DOMParser documents never run scripts or load images.
  const doc = new DOMParser().parseFromString(html, "text/html");
  let budget = MAX_NODES;
  const walk = (n: Node, key: string): ReactNode => {
    if (--budget < 0) return null;
    if (n.nodeType === Node.TEXT_NODE) return n.textContent;
    if (n.nodeType !== Node.ELEMENT_NODE) return null;
    const tag = (n as Element).tagName.toLowerCase();
    if (DROP.has(tag)) return null;
    const kids = [...n.childNodes].map((c, i) => walk(c, `${key}.${i}`));
    const t = ALLOWED[tag];
    if (t === "br") return <br key={key} />;
    if (!t) return <Fragment key={key}>{kids}</Fragment>;
    const Tag = t as "p" | "b" | "i" | "ul" | "ol" | "li";
    return <Tag key={key}>{kids}</Tag>;
  };
  return [...doc.body.childNodes].map((c, i) => walk(c, String(i)));
}

const looksHtml = (s: string) => /<\/?(p|br|b|i|ul|ol|li|strong|em)\b[^>]*>/i.test(s);

export function TextShape({ def, state, conn }: ShapeProps) {
  const b = def.bind ?? {};
  const body = str(bound(state, b, "body"));
  const title = label(state, b, "title");
  return (
    <Frame title={def.label} {...connHead(conn)} right={title ? <Ctx>{title}</Ctx> : undefined}>
      {/* Grows to fit rather than scrolling inside. */}
      <div className="h-full overflow-y-auto break-words text-[15px] leading-snug [&_b]:font-semibold [&_li]:my-0.5 [&_ol]:list-decimal [&_ol]:pl-5 [&_p+p]:mt-2 [&_ul]:list-disc [&_ul]:pl-5">
        {!body ? <span className="text-xs text-dim">Nothing here right now.</span>
          : looksHtml(body) ? safeHtml(body) : <span className="whitespace-pre-wrap">{body}</span>}
      </div>
    </Frame>
  );
}
