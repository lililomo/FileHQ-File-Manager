/** Simple 0–4 strength score (length, character classes, no username). Not a substitute for the server policy. */
export function scorePassword(password: string, username = ''): 0 | 1 | 2 | 3 | 4 {
  if (!password) return 0
  let score = 0
  if (password.length >= 12) score += 1
  if (password.length >= 16) score += 1
  const classes = [/[a-z]/, /[A-Z]/, /[0-9]/, /[^A-Za-z0-9]/].filter((re) =>
    re.test(password),
  ).length
  if (classes >= 3) score += 1
  if (password.length >= 20 || (classes === 4 && password.length >= 14)) score += 1
  if (username && password.toLowerCase().includes(username.toLowerCase()))
    score = Math.max(0, score - 2)
  return Math.min(4, score) as 0 | 1 | 2 | 3 | 4
}
