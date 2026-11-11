// Renders each argument with static/markdown.js against a fake document that
// refuses any HTML parsing (innerHTML, outerHTML, insertAdjacentHTML) and any
// element outside the subset, then prints the trees as JSON. The Go test owns
// the assertions.
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const ALLOWED = new Set(['p', 'br', 'strong', 'em', 'code', 'pre', 'ul', 'ol', 'li']);
const refuse = (what) => () => { throw new Error('refused: ' + what); };

class Node {
  constructor(tag) { this.tagName = tag; this.children = []; }
  appendChild(child) { this.children.push(child); return child; }
  set textContent(v) { this.children = [{ text: String(v) }]; }
  get textContent() { return this.children.map((c) => (c.text !== undefined ? c.text : c.textContent)).join(''); }
  toJSON() { return { tag: this.tagName, children: this.children }; }
}
for (const prop of ['innerHTML', 'outerHTML']) {
  Object.defineProperty(Node.prototype, prop, { set: refuse(prop), get: refuse(prop) });
}
Node.prototype.insertAdjacentHTML = refuse('insertAdjacentHTML');

const document = {
  createElement(tag) {
    if (!ALLOWED.has(tag)) throw new Error('refused element: ' + tag);
    return new Node(tag);
  },
  createTextNode(text) { return { text: String(text) }; },
  createDocumentFragment() { return new Node('#fragment'); },
};
const sandbox = { window: { document }, document };
vm.createContext(sandbox);
vm.runInContext(readFileSync(process.argv[2], 'utf8'), sandbox);
const inputs = JSON.parse(process.argv[3]);
process.stdout.write(JSON.stringify(inputs.map((text) => sandbox.window.coachMarkdown(text, document).children)));
