import type { RenameRule } from './api'

export interface RuleForm {
  find: string
  replace: string
  regex: boolean
  caseInsensitive: boolean
  caseMode: NonNullable<RenameRule['case']>
  numbering: boolean
  pattern: string
  start: string
  step: string
  pad: string
  extMode: NonNullable<RenameRule['ext_mode']>
}

export const DEFAULT_FORM: RuleForm = {
  find: '',
  replace: '',
  regex: false,
  caseInsensitive: false,
  caseMode: 'none',
  numbering: false,
  pattern: '{name}-{n}',
  start: '1',
  step: '1',
  pad: '0',
  extMode: 'keep',
}

/** Form state → API rule (SPEC §8.5 batch rename). */
export function toRule(f: RuleForm): RenameRule {
  const rule: RenameRule = {}
  if (f.find) rule.find = f.find
  if (f.find || f.replace) rule.replace = f.replace
  if (f.regex) rule.regex = true
  if (f.caseInsensitive) rule.case_insensitive = true
  if (f.caseMode !== 'none') rule.case = f.caseMode
  if (f.numbering) {
    rule.numbering = {
      pattern: f.pattern || '{name}-{n}',
      start: Number.parseInt(f.start, 10) || 1,
      step: Number.parseInt(f.step, 10) || 1,
      pad: Math.max(0, Math.min(12, Number.parseInt(f.pad, 10) || 0)),
    }
  }
  if (f.extMode !== 'keep') rule.ext_mode = f.extMode
  return rule
}
