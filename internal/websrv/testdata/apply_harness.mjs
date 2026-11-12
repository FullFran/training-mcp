// Runs static/coach-apply.js with no DOM and prints what coachSuggestions
// finds in each case as JSON. The Go test owns the assertions.
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const [script, casesJSON] = process.argv.slice(2);
const sandbox = {};
vm.createContext(sandbox);
vm.runInContext(readFileSync(script, 'utf8'), sandbox);
const cases = JSON.parse(casesJSON);
process.stdout.write(JSON.stringify(cases.map((c) => sandbox.coachSuggestions(c.text, c.exercises))));
