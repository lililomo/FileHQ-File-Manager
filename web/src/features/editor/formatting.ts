/** Prettier (standalone, lazy chunk) for the languages Monaco cannot format
 *  itself (SPEC §8.13 "format document"; Step 16b). Only the Markdown and
 *  YAML plugins are bundled — PHP has no official standalone plugin. */

const PRETTIER_PARSERS: Record<string, string> = {
  markdown: 'markdown',
  yaml: 'yaml',
}

export function prettierSupports(language: string): boolean {
  return language in PRETTIER_PARSERS
}

export async function formatWithPrettier(
  source: string,
  language: string,
  opts: { tabWidth: number; useTabs: boolean },
): Promise<string> {
  const parser = PRETTIER_PARSERS[language]
  if (!parser) throw new Error(`no prettier parser for ${language}`)
  const [prettier, markdown, yaml] = await Promise.all([
    import('prettier/standalone'),
    import('prettier/plugins/markdown'),
    import('prettier/plugins/yaml'),
  ])
  return prettier.format(source, {
    parser,
    plugins: [markdown.default ?? markdown, yaml.default ?? yaml],
    tabWidth: opts.tabWidth,
    useTabs: opts.useTabs,
    proseWrap: 'preserve',
  })
}
