/** File name / shebang → Monaco language id (SPEC §8.13 step 5). Custom
 *  Monarch grammars (nginx, apache, makefile) are registered by monaco.ts;
 *  everything else is a built-in Monaco language (verified against
 *  monaco-editor 0.57.0 `languages/definitions`). */

const BY_EXT: Record<string, string> = {
  js: 'javascript',
  mjs: 'javascript',
  cjs: 'javascript',
  jsx: 'javascript',
  ts: 'typescript',
  mts: 'typescript',
  cts: 'typescript',
  tsx: 'typescript',
  json: 'json',
  jsonc: 'json',
  json5: 'json',
  webmanifest: 'json',
  html: 'html',
  htm: 'html',
  xhtml: 'html',
  vue: 'html',
  svelte: 'html',
  jsp: 'html',
  ejs: 'html',
  css: 'css',
  scss: 'scss',
  less: 'less',
  md: 'markdown',
  markdown: 'markdown',
  mdx: 'mdx',
  xml: 'xml',
  xsl: 'xml',
  xslt: 'xml',
  svg: 'xml',
  plist: 'xml',
  csproj: 'xml',
  wsdl: 'xml',
  xaml: 'xml',
  yml: 'yaml',
  yaml: 'yaml',
  toml: 'ini',
  ini: 'ini',
  cfg: 'ini',
  env: 'ini',
  properties: 'ini',
  conf: 'ini',
  sh: 'shell',
  bash: 'shell',
  zsh: 'shell',
  fish: 'shell',
  py: 'python',
  pyw: 'python',
  rb: 'ruby',
  php: 'php',
  phtml: 'php',
  go: 'go',
  rs: 'rust',
  java: 'java',
  kt: 'kotlin',
  kts: 'kotlin',
  scala: 'scala',
  c: 'c',
  h: 'c',
  cpp: 'cpp',
  cc: 'cpp',
  cxx: 'cpp',
  hpp: 'cpp',
  hh: 'cpp',
  cs: 'csharp',
  fs: 'fsharp',
  vb: 'vb',
  vbs: 'vb',
  swift: 'swift',
  m: 'objective-c',
  dart: 'dart',
  lua: 'lua',
  pl: 'perl',
  pm: 'perl',
  r: 'r',
  sql: 'sql',
  graphql: 'graphql',
  gql: 'graphql',
  proto: 'protobuf',
  ps1: 'powershell',
  psm1: 'powershell',
  bat: 'bat',
  cmd: 'bat',
  dockerfile: 'dockerfile',
  makefile: 'makefile',
  mk: 'makefile',
  tf: 'hcl',
  hcl: 'hcl',
  clj: 'clojure',
  ex: 'elixir',
  exs: 'elixir',
  txt: 'plaintext',
  log: 'plaintext',
  csv: 'plaintext',
  tsv: 'plaintext',
  v: 'systemverilog',
  sv: 'systemverilog',
  tcl: 'tcl',
  twig: 'twig',
  hbs: 'handlebars',
  pug: 'pug',
  liquid: 'liquid',
  htaccess: 'apache',
  nginx: 'nginx',
}

const BY_NAME: Record<string, string> = {
  dockerfile: 'dockerfile',
  containerfile: 'dockerfile',
  makefile: 'makefile',
  gnumakefile: 'makefile',
  '.env': 'ini',
  '.editorconfig': 'ini',
  '.npmrc': 'ini',
  '.gitignore': 'plaintext',
  '.gitattributes': 'plaintext',
  'nginx.conf': 'nginx',
  'mime.types': 'nginx',
  '.htaccess': 'apache',
  'httpd.conf': 'apache',
  'apache2.conf': 'apache',
  'composer.json': 'json',
  'package.json': 'json',
  'tsconfig.json': 'json',
}

const BY_SHEBANG: [RegExp, string][] = [
  [/\b(bash|sh|zsh|ksh|dash)\b/, 'shell'],
  [/\bpython[0-9.]*\b/, 'python'],
  [/\b(node|deno|bun)\b/, 'javascript'],
  [/\bphp\b/, 'php'],
  [/\bruby\b/, 'ruby'],
  [/\bperl\b/, 'perl'],
]

/** Languages offered in the status-bar picker (id → label). */
export const LANGUAGE_OPTIONS: { id: string; label: string }[] = [
  { id: 'plaintext', label: 'Plain Text' },
  { id: 'apache', label: 'Apache' },
  { id: 'bat', label: 'Batch' },
  { id: 'c', label: 'C' },
  { id: 'cpp', label: 'C++' },
  { id: 'csharp', label: 'C#' },
  { id: 'css', label: 'CSS' },
  { id: 'dart', label: 'Dart' },
  { id: 'dockerfile', label: 'Dockerfile' },
  { id: 'elixir', label: 'Elixir' },
  { id: 'go', label: 'Go' },
  { id: 'graphql', label: 'GraphQL' },
  { id: 'handlebars', label: 'Handlebars' },
  { id: 'hcl', label: 'HCL / Terraform' },
  { id: 'html', label: 'HTML' },
  { id: 'ini', label: 'INI / .env' },
  { id: 'java', label: 'Java' },
  { id: 'javascript', label: 'JavaScript' },
  { id: 'json', label: 'JSON' },
  { id: 'kotlin', label: 'Kotlin' },
  { id: 'less', label: 'Less' },
  { id: 'lua', label: 'Lua' },
  { id: 'makefile', label: 'Makefile' },
  { id: 'markdown', label: 'Markdown' },
  { id: 'nginx', label: 'Nginx' },
  { id: 'objective-c', label: 'Objective-C' },
  { id: 'perl', label: 'Perl' },
  { id: 'php', label: 'PHP' },
  { id: 'powershell', label: 'PowerShell' },
  { id: 'protobuf', label: 'Protocol Buffers' },
  { id: 'pug', label: 'Pug' },
  { id: 'python', label: 'Python' },
  { id: 'r', label: 'R' },
  { id: 'ruby', label: 'Ruby' },
  { id: 'rust', label: 'Rust' },
  { id: 'scala', label: 'Scala' },
  { id: 'scss', label: 'SCSS' },
  { id: 'shell', label: 'Shell' },
  { id: 'sql', label: 'SQL' },
  { id: 'swift', label: 'Swift' },
  { id: 'systemverilog', label: 'Verilog / SystemVerilog' },
  { id: 'tcl', label: 'Tcl' },
  { id: 'twig', label: 'Twig' },
  { id: 'typescript', label: 'TypeScript' },
  { id: 'vb', label: 'VB / VBScript' },
  { id: 'xml', label: 'XML' },
  { id: 'yaml', label: 'YAML' },
]

export function languageLabel(id: string): string {
  return LANGUAGE_OPTIONS.find((l) => l.id === id)?.label ?? id
}

/** Shebang, special file names, compound names, then the extension. */
export function detectLanguage(name: string, firstLine = ''): string {
  if (firstLine.startsWith('#!')) {
    for (const [re, id] of BY_SHEBANG) if (re.test(firstLine)) return id
  }
  const lower = name.toLowerCase()
  const byName = BY_NAME[lower]
  if (byName) return byName
  if (lower.startsWith('.env')) return 'ini'
  if (lower.endsWith('.blade.php')) return 'php'
  const dot = lower.lastIndexOf('.')
  const ext = dot >= 0 ? lower.slice(dot + 1) : ''
  return (ext && BY_EXT[ext]) || 'plaintext'
}

/** Files the file manager opens in the editor with "Buka" (text and code). */
export function isEditableByName(name: string, mime: string | null, category: string): boolean {
  if (category === 'code' || category === 'text') return true
  if (
    mime &&
    (mime.startsWith('text/') ||
      mime === 'application/json' ||
      mime.endsWith('+xml') ||
      mime.endsWith('+json'))
  )
    return true
  return detectLanguage(name) !== 'plaintext'
}
