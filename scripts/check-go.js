#!/usr/bin/env node
'use strict';
/* ---------------------------------------------------------------------------
 * check-go.js — بررسی ساختاری قراردادها بدون کامپایلر Go
 *
 * چرا وجود دارد: در محیط تولید کد، کامپایلر Go در دسترس نیست. اولین باری که
 * کد به کامپایلر می‌رسد، روی سرور و وسط deploy است — بدترین جای ممکن برای
 * کشف یک پرانتز نبسته. این اسکریپت کلاس خطاهایی را می‌گیرد که کامپایلر اول
 * از همه می‌گیرد و بدون اجرای کامپایلر قابل تشخیص‌اند.
 *
 * ⚠️ این کامپایلر نیست. type checking نمی‌کند. اگر این پاس شود هنوز ممکن
 * است `go build` خطا بدهد. ولی اگر این رد شود، `go build` قطعاً رد می‌شود.
 *
 * نکته مهم درباره ساختار: هر قرارداد از دو فایل ساخته می‌شود، chaincode.go و
 * shared.go، که Go هر دو را یک package می‌بیند. پس بررسی باید هر دو را با هم
 * ببیند وگرنه هر کمکی مشترک را «تعریف‌نشده» گزارش می‌کند.
 *
 * استفاده: node check-go.js <chaincode-dir> [<shared.go>]
 * --------------------------------------------------------------------------- */

const fs = require('fs');
const path = require('path');

const ROOT = process.argv[2] || path.join(__dirname, '..', 'chaincode');
const SHARED_ARG = process.argv[3];

let fail = 0;
const bad = (name, msg) => { fail++; console.log(`  ✗ ${name}: ${msg}`); };
const warn = (name, msg) => console.log(`  ⚠ ${name}: ${msg}`);

/* رشته‌ها، runeها و کامنت‌ها را حذف می‌کند تا جداکننده‌های داخلشان شمرده
   نشوند. متن فارسی داخل رشته‌ها پر از پرانتز است و بدون این کار هر فایل
   نامتوازن به نظر می‌رسد. */
function strip(go) {
  let out = '', i = 0;
  while (i < go.length) {
    const c = go[i];
    if (c === '/' && go[i + 1] === '/') { while (i < go.length && go[i] !== '\n') i++; continue; }
    if (c === '/' && go[i + 1] === '*') { i += 2; while (i < go.length && !(go[i] === '*' && go[i + 1] === '/')) i++; i += 2; continue; }
    if (c === '`') { i++; while (i < go.length && go[i] !== '`') i++; i++; out += '""'; continue; }
    if (c === '"') { i++; while (i < go.length && !(go[i] === '"' && go[i - 1] !== '\\')) i++; i++; out += '""'; continue; }
    if (c === "'") { i++; while (i < go.length && !(go[i] === "'" && go[i - 1] !== '\\')) i++; i++; out += "''"; continue; }
    out += c; i++;
  }
  return out;
}

function checkDelimiters(name, code) {
  const pairs = { '}': '{', ')': '(', ']': '[' };
  const stack = [];
  let line = 1;
  for (const ch of code) {
    if (ch === '\n') line++;
    else if ('{(['.includes(ch)) stack.push({ ch, line });
    else if ('})]'.includes(ch)) {
      const top = stack.pop();
      if (!top) return bad(name, `«${ch}» اضافی در خط ${line}`);
      if (top.ch !== pairs[ch]) return bad(name, `«${top.ch}» خط ${top.line} با «${ch}» خط ${line} بسته شده`);
    }
  }
  if (stack.length) bad(name, `«${stack[stack.length - 1].ch}» خط ${stack[stack.length - 1].line} بسته نشده`);
}

/* اسامی سطح بالا: توابع، متدها، انواع، ثابت‌ها و متغیرها. */
function topLevelDecls(code) {
  const fns = new Set(), types = new Set(), vals = new Set(), methods = new Map();
  for (const m of code.matchAll(/^func\s+(\w+)\s*\(/gm)) fns.add(m[1]);
  for (const m of code.matchAll(/^func\s+\((\w+)\s+\*?(\w+)\)\s+(\w+)\s*\(/gm)) {
    if (!methods.has(m[2])) methods.set(m[2], new Set());
    methods.get(m[2]).add(m[3]);
  }
  for (const m of code.matchAll(/^type\s+(\w+)\s/gm)) types.add(m[1]);
  for (const m of code.matchAll(/^(?:var|const)\s+(\w+)\s*=/gm)) vals.add(m[1]);
  for (const m of code.matchAll(/^(?:var|const)\s+(\w+)\s+[\w\[\]*.]/gm)) vals.add(m[1]);
  // بلوک‌های const (...) و var (...)
  for (const blk of code.matchAll(/^(?:var|const)\s*\(([\s\S]*?)^\)/gm)) {
    for (const m of blk[1].matchAll(/^\s+(\w+)\s*(?:=|\w)/gm)) vals.add(m[1]);
  }
  return { fns, types, vals, methods };
}

const GO_BUILTIN = new Set([
  'append', 'cap', 'close', 'complex', 'copy', 'delete', 'imag', 'len', 'make',
  'new', 'panic', 'print', 'println', 'real', 'recover', 'min', 'max', 'clear',
  'string', 'int', 'int32', 'int64', 'uint', 'uint32', 'uint64', 'float64',
  'byte', 'rune', 'bool', 'error', 'func', 'if', 'for', 'switch', 'return',
  'go', 'defer', 'range', 'select', 'case', 'else', 'map', 'chan', 'struct',
  'interface', 'type', 'var', 'const', 'import', 'package', 'break', 'continue',
]);

/* --------------------------------------------------------------------------- */

const contracts = fs.readdirSync(ROOT)
  .filter((d) => !d.startsWith('_') && fs.statSync(path.join(ROOT, d)).isDirectory())
  .sort();

const sharedPath = SHARED_ARG || path.join(ROOT, '_shared', 'shared.go');
if (!fs.existsSync(sharedPath)) { console.log(`فایل مشترک یافت نشد: ${sharedPath}`); process.exit(1); }
const sharedRaw = fs.readFileSync(sharedPath, 'utf8');
const sharedCode = strip(sharedRaw);
const sharedDecls = topLevelDecls(sharedCode);

console.log(`بررسی ${contracts.length} قرارداد + فایل مشترک\n`);

/* --- فایل مشترک --- */
checkDelimiters('shared.go', sharedCode);
if (!/^package main$/m.test(sharedRaw)) bad('shared.go', 'package main نیست');
if (/func main\(/.test(sharedCode)) bad('shared.go', 'نباید main داشته باشد — در هر قرارداد تکراری می‌شود');

/* قواعد قطعیت. هر تراکنش روی همه peerها باید بایت‌به‌بایت یکسان شود. */
function checkDeterminism(name, raw, code) {
  if (/\btime\.Now\(/.test(code)) bad(name, 'time.Now() — ساعت هر peer متفاوت است، از txTime(ctx) استفاده کنید');
  if (/\bmath\/rand\b|\brand\./.test(code)) bad(name, 'تولید عدد تصادفی — غیرقطعی');
  if (/\bmath\.\w/.test(code)) bad(name, 'بسته math — محاسبه اعشاری غیرقطعی است');
  if (/\bfloat(32|64)\b/.test(code)) bad(name, 'نوع float — گرد کردن بین معماری‌ها فرق می‌کند');
  if (/\bos\.Getenv\(|\bos\.Hostname\(/.test(code)) bad(name, 'خواندن محیط — بین peerها متفاوت است');
  if (/\bgo\s+func\b/.test(code)) bad(name, 'گوروتین — ترتیب اجرا قطعی نیست');

  /* پیمایش map بدون ترتیب ثابت. Go عمداً ترتیب map را تصادفی می‌کند، پس
     دو peer دو خروجی متفاوت می‌سازند و تراکنش رد می‌شود. این خطا در پروژه
     6G سه بار تکرار شد و هر بار فقط زیر بار همزمانی خودش را نشان داد. */
  for (const m of code.matchAll(/for\s+[\w,\s]+:=\s*range\s+(\w+)/g)) {
    const v = m[1];
    // مرزهای واژه لازم‌اند: بدون آن‌ها «var prices map[» برای متغیری به نام
    // «s» هم مطابقت می‌کند و هشدار کاذب می‌دهد.
    const isMap = new RegExp(`(?:^|[^\\w])${v}\\s*(?::?=\\s*|\\s+)map\\[`).test(code)
      || new RegExp(`\\bvar\\s+${v}\\s+map\\[`).test(code)
      || (sharedDecls.vals.has(v) && new RegExp(`^var\\s+${v}\\s*=\\s*map\\[`, 'm').test(sharedCode));
    if (isMap) {
      const idx = m.index;
      // الگوی رایج «کلیدها را جمع کن، مرتب کن، بعد پیمایش کن» sort را *بعد*
      // از حلقه دارد، پس هر دو طرف باید دیده شود.
      const ctx = code.slice(Math.max(0, idx - 400), idx + 400);
      if (!/sort\.(Strings|Slice|Ints)/.test(ctx) && !/AllUses/.test(code.slice(idx, idx + 200))) {
        warn(name, `پیمایش map «${v}» — اگر خروجی به حالت اثر دارد باید کلیدها مرتب شوند`);
      }
    }
  }
}
checkDeterminism('shared.go', sharedRaw, sharedCode);

/* --- هر قرارداد --- */
for (const cc of contracts) {
  const file = path.join(ROOT, cc, 'chaincode.go');
  if (!fs.existsSync(file)) { bad(cc, 'chaincode.go ندارد'); continue; }
  const raw = fs.readFileSync(file, 'utf8');
  const code = strip(raw);

  checkDelimiters(cc, code);
  checkDeterminism(cc, raw, code);

  if (!/^package main$/m.test(raw)) bad(cc, 'package main نیست');

  const decls = topLevelDecls(code);

  /* تعریف تکراری بین chaincode.go و shared.go. چون Go هر دو را یک package
     می‌بیند، هم‌نامی سطح بالا خطای کامپایل است — و چون دو فایل جدا هستند،
     موقع نوشتن کد به چشم نمی‌آید. */
  for (const set of ['fns', 'types', 'vals']) {
    for (const n of decls[set]) {
      if (sharedDecls[set].has(n)) bad(cc, `«${n}» هم در chaincode.go و هم در shared.go تعریف شده`);
    }
  }

  /* main و ثبت قرارداد */
  if (!/func main\(\)/.test(code)) bad(cc, 'تابع main ندارد');
  const ctype = `${cc}Contract`;
  if (!decls.types.has(ctype)) bad(cc, `نوع ${ctype} تعریف نشده`);
  if (!new RegExp(`NewChaincode\\(&${ctype}\\{\\}\\)`).test(code)) bad(cc, `main قرارداد ${ctype} را ثبت نمی‌کند`);
  if (!new RegExp(`type ${ctype} struct \\{[\\s\\S]{0,200}?NetworkBase`).test(code)) bad(cc, `${ctype} باید NetworkBase را جاسازی کند`);

  /* importها: هر کدام باید استفاده شود و هر بسته استفاده‌شده import شده باشد. */
  const impBlock = (raw.match(/^import \(([\s\S]*?)^\)/m) || ['', ''])[1];
  const imports = [...impBlock.matchAll(/"([^"]+)"/g)].map((m) => m[1]);
  const body = code.replace(/^import \([\s\S]*?^\)/m, '');
  for (const imp of imports) {
    const pkg = imp.split('/').pop();
    if (pkg === 'contractapi') continue;
    if (!new RegExp(`\\b${pkg}\\.`).test(body)) bad(cc, `بسته «${imp}» import شده ولی استفاده نشده`);
  }
  const usedPkgs = new Set([...body.matchAll(/(?:^|[^.\w])(json|fmt|log|strings|strconv|sort|sha256|hex|errors|time)\./g)].map((m) => m[1]));
  const impNames = new Set(imports.map((i) => i.split('/').pop()));
  for (const p of usedPkgs) {
    if (!impNames.has(p)) bad(cc, `بسته «${p}» استفاده شده ولی import نشده`);
  }

  /* توابع صداشده که هیچ‌جا تعریف نشده‌اند. متغیرهای محلی از نوع تابع و
     متدها کنار گذاشته می‌شوند تا هشدار کاذب ندهد. */
  const localVars = new Set([...code.matchAll(/(\w+)\s*:?=\s*func\s*\(/g)].map((m) => m[1]));
  const known = new Set([
    ...decls.fns, ...sharedDecls.fns, ...GO_BUILTIN, ...localVars,
    ...decls.types, ...sharedDecls.types,
  ]);
  const seen = new Set();
  const noComments = code.split('\n').map((l) => l.replace(/\/\/.*$/, '')).join('\n');
  for (const m of noComments.matchAll(/(?:^|[^.\w)\]])([a-z]\w*)\s*\(/gm)) {
    const fn = m[1];
    if (known.has(fn) || seen.has(fn)) continue;
    seen.add(fn);
    bad(cc, `تابع ${fn}() صدا زده شده ولی تعریف نشده`);
  }

  /* هر متد صادرشده قرارداد باید ctx را اولین پارامتر بگیرد، وگرنه
     contractapi آن را در زمان اجرا رد می‌کند نه در زمان کامپایل. */
  for (const m of code.matchAll(new RegExp(`^func \\((?:\\w+) \\*${ctype}\\) ([A-Z]\\w*)\\(([^)]*)\\)`, 'gm'))) {
    if (!/contractapi\.TransactionContextInterface/.test(m[2])) {
      bad(cc, `متد ${m[1]} پارامتر ctx ندارد — contractapi آن را رد می‌کند`);
    }
  }
}

console.log(fail ? `\n✗ ${fail} مشکل` : '\n✅ همه بررسی‌های ساختاری پاس شد');
process.exit(fail ? 1 : 0);
