// A small Markdown subset for the coach's replies: paragraphs, line breaks,
// **bold**, *italics*, `code`, fenced code blocks and lists. Everything is
// built with createElement and text nodes, never by parsing HTML, so anything
// the model writes (a <script>, an <img onerror>) stays plain text.
(function (root) {
  'use strict';

  // Code first, then bold, then italics. Italics need a non-space right inside
  // both asterisks so "3 * 4 * 5" stays as it is; underscores are left alone
  // because tool names like live_workout are full of them.
  const INLINE = /(`[^`\n]+`)|(\*\*(?=\S)[^\n]*?\S\*\*)|(\*(?=[^\s*])[^*\n]*?[^\s*]\*)/g;

  function inline(doc, parent, text) {
    const re = new RegExp(INLINE.source, 'g');
    let last = 0;
    let m;
    while ((m = re.exec(text)) !== null) {
      if (m.index > last) parent.appendChild(doc.createTextNode(text.slice(last, m.index)));
      const token = m[0];
      if (m[1]) {
        const code = doc.createElement('code');
        code.textContent = token.slice(1, -1);
        parent.appendChild(code);
      } else if (m[2]) {
        const strong = doc.createElement('strong');
        inline(doc, strong, token.slice(2, -2));
        parent.appendChild(strong);
      } else {
        const em = doc.createElement('em');
        em.textContent = token.slice(1, -1);
        parent.appendChild(em);
      }
      last = m.index + token.length;
    }
    if (last < text.length) parent.appendChild(doc.createTextNode(text.slice(last)));
  }

  const ITEM = /^\s*(?:([-*•])|(\d+)[.)])\s+(.*)$/;
  const FENCE = /^\s*```/;
  const HEADING = /^#{1,6}\s+(.*)$/;

  // render returns a fragment for text; doc defaults to the page's document.
  function render(text, doc) {
    doc = doc || root.document;
    const out = doc.createDocumentFragment();
    const lines = String(text || '').replace(/\r\n?/g, '\n').split('\n');
    let para = null;
    let list = null;
    for (let i = 0; i < lines.length; i++) {
      const line = lines[i];
      if (FENCE.test(line)) {
        const body = [];
        for (i++; i < lines.length && !FENCE.test(lines[i]); i++) body.push(lines[i]);
        const pre = doc.createElement('pre');
        const code = doc.createElement('code');
        code.textContent = body.join('\n');
        pre.appendChild(code);
        out.appendChild(pre);
        para = null;
        list = null;
        continue;
      }
      if (!line.trim()) { para = null; list = null; continue; }
      const item = ITEM.exec(line);
      if (item) {
        const tag = item[1] ? 'ul' : 'ol';
        if (!list || list.tagName.toLowerCase() !== tag) {
          list = doc.createElement(tag);
          out.appendChild(list);
        }
        const li = doc.createElement('li');
        inline(doc, li, item[3]);
        list.appendChild(li);
        para = null;
        continue;
      }
      list = null;
      const heading = HEADING.exec(line);
      if (heading) {
        const p = doc.createElement('p');
        const strong = doc.createElement('strong');
        inline(doc, strong, heading[1]);
        p.appendChild(strong);
        out.appendChild(p);
        para = null;
        continue;
      }
      if (para) {
        para.appendChild(doc.createElement('br'));
      } else {
        para = doc.createElement('p');
        out.appendChild(para);
      }
      inline(doc, para, line);
    }
    return out;
  }

  root.coachMarkdown = render;
})(typeof window !== 'undefined' ? window : globalThis);
