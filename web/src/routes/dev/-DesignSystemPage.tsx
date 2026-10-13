/* Design gallery (/dev/design): every token and component in light and dark side by side.
   Dev-only (see vite `define` __DESIGN_PAGE__); strings here are intentionally not translated. */
import { useEffect, useRef, useState } from 'react'
import { Download, Folder, Plus, Search, Settings, Trash2, Upload } from 'lucide-react'
import { toast } from 'sonner'
import { EmptyState } from '@/components/EmptyState'
import { FILE_CATEGORIES } from '@/components/file-icon/categories'
import { FileIcon } from '@/components/file-icon/FileIcon'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from '@/components/ui/alert-dialog'
import { Badge } from '@/components/ui/badge'
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from '@/components/ui/breadcrumb'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from '@/components/ui/command'
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from '@/components/ui/context-menu'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import { Kbd, KbdGroup } from '@/components/ui/kbd'
import { Label } from '@/components/ui/label'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Progress } from '@/components/ui/progress'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Separator } from '@/components/ui/separator'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from '@/components/ui/sheet'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Textarea } from '@/components/ui/textarea'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { contrastRatio } from '@/lib/contrast'
import { ACCENTS, DENSITIES, THEME_PREFS, isAccent, isDensity, isThemePref } from '@/lib/theme'
import { useUIStore } from '@/stores/ui'

const COLOR_TOKENS = [
  '--bg',
  '--surface',
  '--surface-2',
  '--border',
  '--text',
  '--text-muted',
  '--accent',
  '--accent-fg',
  '--accent-subtle',
  '--success',
  '--warning',
  '--danger',
  '--focus',
] as const
const PAIRS: [string, string, number][] = [
  ['--text', '--bg', 4.5],
  ['--text-muted', '--bg', 4.5],
  ['--text', '--surface', 4.5],
  ['--text', '--surface-2', 4.5],
  ['--accent-fg', '--accent', 4.5],
  ['--text', '--accent-subtle', 4.5],
  ['--danger', '--bg', 4.5],
  ['--accent', '--bg', 3],
  ['--success', '--bg', 3],
  ['--warning', '--bg', 3],
]

function toHex(value: string): string {
  const v = value.trim()
  if (v.startsWith('#')) return v
  const m = v.match(/rgba?\((\d+)[,\s]+(\d+)[,\s]+(\d+)/)
  if (!m) return v
  return '#' + [m[1], m[2], m[3]].map((n) => Number(n).toString(16).padStart(2, '0')).join('')
}

function safeRatio(fg: string | undefined, bg: string | undefined): number | null {
  if (!fg || !bg) return null
  try {
    return contrastRatio(fg, bg)
  } catch {
    return null
  }
}

function TokenPanel({ theme, accent }: { theme: 'light' | 'dark'; accent: string }) {
  const ref = useRef<HTMLDivElement>(null)
  const [values, setValues] = useState<Record<string, string>>({})
  useEffect(() => {
    const el = ref.current
    if (!el) return
    const cs = getComputedStyle(el)
    const out: Record<string, string> = {}
    for (const t of COLOR_TOKENS) out[t] = toHex(cs.getPropertyValue(t))
    setValues(out)
  }, [theme, accent])
  return (
    <div
      ref={ref}
      data-theme={theme}
      data-accent={accent === 'blue' ? undefined : accent}
      className="rounded-lg border border-border bg-bg p-4 text-text"
    >
      <h3 className="mb-3 text-base font-semibold">{theme}</h3>
      <div className="grid grid-cols-2 gap-2 text-xs sm:grid-cols-3">
        {COLOR_TOKENS.map((t) => (
          <div
            key={t}
            className="flex items-center gap-2 rounded-md border border-border bg-surface p-2"
          >
            <span
              className="size-7 shrink-0 rounded-md border border-border"
              style={{ background: `var(${t})` }}
            />
            <div className="min-w-0">
              <div className="truncate font-mono">{t}</div>
              <div className="truncate text-text-muted">{values[t] ?? '…'}</div>
            </div>
          </div>
        ))}
      </div>
      <table className="mt-3 w-full text-xs">
        <thead className="text-left text-text-muted">
          <tr>
            <th className="py-1 font-medium">pair</th>
            <th className="py-1 font-medium">ratio</th>
            <th className="py-1 font-medium">min</th>
          </tr>
        </thead>
        <tbody>
          {PAIRS.map(([fg, bg, min]) => {
            const f = values[fg]
            const b = values[bg]
            const ratio = safeRatio(f, b)
            const ok = ratio !== null && ratio >= min
            return (
              <tr key={fg + bg} className="border-t border-border">
                <td className="py-1 font-mono">
                  {fg} / {bg}
                </td>
                <td className={ok ? 'py-1 text-success' : 'py-1 text-danger'}>
                  {ratio ? ratio.toFixed(2) : '—'}
                </td>
                <td className="py-1 text-text-muted">{min}</td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="space-y-3">
      <h3 className="text-sm font-semibold text-text-muted uppercase tracking-wide">{title}</h3>
      {children}
    </section>
  )
}

function Gallery() {
  const [checked, setChecked] = useState(true)
  return (
    <div className="space-y-8">
      <Section title="Typography">
        <p className="text-xs">12 px — meta, status bar</p>
        <p className="text-sm">13 px — tabel, menu</p>
        <p className="text-base">
          14 px — UI umum (400 / <span className="font-medium">500</span> /{' '}
          <span className="font-semibold">600</span>)
        </p>
        <p className="text-lg font-semibold">16 px — judul dialog</p>
        <p className="text-xl font-semibold">20 px — judul halaman</p>
        <p className="font-mono text-sm">/Proyek/website/assets · rwxr-xr-x · 755</p>
        <p className="tabular text-sm">1.234.567 · 12 Okt 2026 · 6,2 GB</p>
      </Section>
      <Section title="Buttons">
        <div className="flex flex-wrap items-center gap-2">
          <Button>Primary</Button>
          <Button variant="secondary">Secondary</Button>
          <Button variant="outline">Outline</Button>
          <Button variant="ghost">Ghost</Button>
          <Button variant="danger">Danger</Button>
          <Button variant="link">Link</Button>
          <Button disabled>Disabled</Button>
          <Button size="sm">Small</Button>
          <Button size="lg">Large</Button>
          <Button>
            <Plus /> With icon
          </Button>
          <IconButton label="Upload" shortcut="Ctrl+U">
            <Upload />
          </IconButton>
          <IconButton label="Download">
            <Download />
          </IconButton>
          <IconButton label="Delete" className="text-danger hover:text-danger">
            <Trash2 />
          </IconButton>
        </div>
      </Section>
      <Section title="Inputs">
        <div className="grid max-w-md gap-3">
          <div className="space-y-1.5">
            <Label htmlFor="g-input">Nama file</Label>
            <Input id="g-input" placeholder="laporan.pdf" />
          </div>
          <Input aria-invalid placeholder="invalid" defaultValue="CON" />
          <Input disabled placeholder="disabled" />
          <Textarea placeholder="Catatan…" rows={2} />
          <div className="flex items-center gap-2">
            <Checkbox
              id="g-check"
              checked={checked}
              onCheckedChange={(v) => setChecked(v === true)}
            />
            <Label htmlFor="g-check">Folder di atas</Label>
          </div>
          <div className="flex items-center gap-2">
            <Switch id="g-switch" defaultChecked />
            <Label htmlFor="g-switch">Ingat view per folder</Label>
          </div>
          <RadioGroup defaultValue="details" className="flex gap-4">
            {['details', 'list', 'tiles'].map((v) => (
              <div key={v} className="flex items-center gap-2">
                <RadioGroupItem value={v} id={`g-radio-${v}`} />
                <Label htmlFor={`g-radio-${v}`}>{v}</Label>
              </div>
            ))}
          </RadioGroup>
          <Select defaultValue="name">
            <SelectTrigger className="w-48">
              <SelectValue placeholder="Urutkan" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="name">Nama</SelectItem>
              <SelectItem value="size">Ukuran</SelectItem>
              <SelectItem value="mtime">Diubah</SelectItem>
            </SelectContent>
          </Select>
        </div>
      </Section>
      <Section title="Navigation">
        <Breadcrumb>
          <BreadcrumbList>
            <BreadcrumbItem>
              <BreadcrumbLink href="#">File Saya</BreadcrumbLink>
            </BreadcrumbItem>
            <BreadcrumbSeparator />
            <BreadcrumbItem>
              <BreadcrumbLink href="#">Proyek</BreadcrumbLink>
            </BreadcrumbItem>
            <BreadcrumbSeparator />
            <BreadcrumbItem>
              <BreadcrumbPage>assets</BreadcrumbPage>
            </BreadcrumbItem>
          </BreadcrumbList>
        </Breadcrumb>
        <Tabs defaultValue="public">
          <TabsList>
            <TabsTrigger value="public">Link publik</TabsTrigger>
            <TabsTrigger value="users">Ke user</TabsTrigger>
          </TabsList>
          <TabsContent value="public" className="text-sm text-text-muted">
            Konten tab 1
          </TabsContent>
          <TabsContent value="users" className="text-sm text-text-muted">
            Konten tab 2
          </TabsContent>
        </Tabs>
        <div className="flex flex-wrap items-center gap-2">
          <KbdGroup>
            <Kbd>Ctrl</Kbd>
            <Kbd>K</Kbd>
          </KbdGroup>
          <Badge>Badge</Badge>
          <Badge variant="secondary">Secondary</Badge>
          <Badge variant="outline">Outline</Badge>
          <Badge variant="destructive">Danger</Badge>
        </div>
      </Section>
      <Section title="Feedback">
        <Progress value={62} className="max-w-md" />
        <div className="flex max-w-md gap-2">
          <Skeleton className="h-4 w-1/3" />
          <Skeleton className="h-4 w-1/4" />
          <Skeleton className="h-4 w-1/5" />
        </div>
        <div className="flex flex-wrap gap-2">
          <Button variant="secondary" size="sm" onClick={() => toast.success('Upload selesai')}>
            Toast sukses
          </Button>
          <Button
            variant="secondary"
            size="sm"
            onClick={() => toast.error('Gagal memindahkan 2 file')}
          >
            Toast error
          </Button>
          <Button
            variant="secondary"
            size="sm"
            onClick={() =>
              toast('3 file dipindahkan', {
                action: { label: 'Urungkan', onClick: () => {} },
                duration: 10_000,
              })
            }
          >
            Toast undo
          </Button>
        </div>
        <EmptyState
          icon={Folder}
          title="Folder ini kosong"
          description="Seret file ke sini atau klik Upload."
          action={<Button size="sm">Upload</Button>}
          className="rounded-lg border border-dashed"
        />
      </Section>
      <Section title="Overlays">
        <div className="flex flex-wrap gap-2">
          <Dialog>
            <DialogTrigger asChild>
              <Button variant="secondary" size="sm">
                Dialog
              </Button>
            </DialogTrigger>
            <DialogContent>
              <DialogHeader>
                <DialogTitle>Folder baru</DialogTitle>
                <DialogDescription>Nama folder di dalam /Proyek.</DialogDescription>
              </DialogHeader>
              <Input placeholder="Nama folder" />
              <DialogFooter>
                <Button variant="ghost">Batal</Button>
                <Button>Buat</Button>
              </DialogFooter>
            </DialogContent>
          </Dialog>
          <AlertDialog>
            <AlertDialogTrigger asChild>
              <Button variant="danger" size="sm">
                Alert dialog
              </Button>
            </AlertDialogTrigger>
            <AlertDialogContent>
              <AlertDialogHeader>
                <AlertDialogTitle>Hapus permanen 3 item?</AlertDialogTitle>
                <AlertDialogDescription>Tindakan ini tidak bisa diurungkan.</AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel>Batal</AlertDialogCancel>
                <AlertDialogAction>Hapus</AlertDialogAction>
              </AlertDialogFooter>
            </AlertDialogContent>
          </AlertDialog>
          <Sheet>
            <SheetTrigger asChild>
              <Button variant="secondary" size="sm">
                Sheet
              </Button>
            </SheetTrigger>
            <SheetContent>
              <SheetHeader>
                <SheetTitle>Properties</SheetTitle>
                <SheetDescription>logo.png · 24 KB</SheetDescription>
              </SheetHeader>
            </SheetContent>
          </Sheet>
          <Popover>
            <PopoverTrigger asChild>
              <Button variant="secondary" size="sm">
                Popover
              </Button>
            </PopoverTrigger>
            <PopoverContent className="text-sm">Isi popover</PopoverContent>
          </Popover>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="secondary" size="sm">
                Dropdown
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent>
              <DropdownMenuItem>
                <Settings /> Pengaturan
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem variant="destructive">
                <Trash2 /> Hapus
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
          <ContextMenu>
            <ContextMenuTrigger asChild>
              <Button variant="outline" size="sm">
                Klik kanan di sini
              </Button>
            </ContextMenuTrigger>
            <ContextMenuContent>
              <ContextMenuItem>Buka</ContextMenuItem>
              <ContextMenuItem>Rename</ContextMenuItem>
              <ContextMenuSeparator />
              <ContextMenuItem variant="destructive">Delete</ContextMenuItem>
            </ContextMenuContent>
          </ContextMenu>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button variant="secondary" size="sm">
                Tooltip
              </Button>
            </TooltipTrigger>
            <TooltipContent>
              Tooltip dengan shortcut <Kbd className="ml-1">F2</Kbd>
            </TooltipContent>
          </Tooltip>
        </div>
        <Command className="max-w-md rounded-lg border border-border">
          <CommandInput placeholder="Lompat ke folder atau jalankan aksi…" />
          <CommandList>
            <CommandEmpty>Tidak ada hasil.</CommandEmpty>
            <CommandGroup heading="Aksi">
              <CommandItem>
                <Upload /> Upload file
              </CommandItem>
              <CommandItem>
                <Search /> Cari isi file
              </CommandItem>
            </CommandGroup>
          </CommandList>
        </Command>
      </Section>
      <Section title="Table">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Nama</TableHead>
              <TableHead className="text-right">Ukuran</TableHead>
              <TableHead>Tipe</TableHead>
              <TableHead>Diubah</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {[
              ['css', 'folder', '—', 'Folder', '2 jam lalu'],
              ['logo.png', 'image', '24 KB', 'Gambar PNG', 'kemarin'],
              ['index.html', 'code', '3 KB', 'HTML', '12 Okt 2026'],
            ].map(([name, cat, size, type, when]) => (
              <TableRow key={name}>
                <TableCell className="flex items-center gap-2 font-medium">
                  <FileIcon category={cat!} /> {name}
                </TableCell>
                <TableCell className="tabular text-right">{size}</TableCell>
                <TableCell>{type}</TableCell>
                <TableCell className="text-text-muted">{when}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </Section>
      <Section title="File icons">
        {[16, 24, 48, 96].map((size) => (
          <div key={size} className="flex flex-wrap items-end gap-3">
            {FILE_CATEGORIES.map((c) => (
              <div key={c} className="flex flex-col items-center gap-1 text-[10px] text-text-muted">
                <FileIcon category={c} size={size} />
                {size === 16 ? <span>{c}</span> : null}
              </div>
            ))}
          </div>
        ))}
      </Section>
    </div>
  )
}

export default function DesignSystemPage() {
  const theme = useUIStore((s) => s.theme)
  const accent = useUIStore((s) => s.accent)
  const density = useUIStore((s) => s.density)
  const { setTheme, setAccent, setDensity } = useUIStore.getState()
  return (
    <div className="min-h-dvh bg-bg p-6 text-text">
      <header className="mb-6 flex flex-wrap items-center gap-4">
        <h1 className="text-xl font-semibold">Design system</h1>
        <Separator orientation="vertical" className="h-6" />
        <RadioGroup
          value={theme}
          onValueChange={(v) => isThemePref(v) && setTheme(v)}
          className="flex gap-3"
        >
          {THEME_PREFS.map((p) => (
            <div key={p} className="flex items-center gap-1.5">
              <RadioGroupItem value={p} id={`theme-${p}`} />
              <Label htmlFor={`theme-${p}`}>{p}</Label>
            </div>
          ))}
        </RadioGroup>
        <Select value={accent} onValueChange={(v) => isAccent(v) && setAccent(v)}>
          <SelectTrigger className="w-36">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {ACCENTS.map((a) => (
              <SelectItem key={a} value={a}>
                {a}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Select value={density} onValueChange={(v) => isDensity(v) && setDensity(v)}>
          <SelectTrigger className="w-36">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {DENSITIES.map((d) => (
              <SelectItem key={d} value={d}>
                {d}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </header>
      <Section title="Tokens">
        <div className="grid gap-4 lg:grid-cols-2">
          <TokenPanel theme="light" accent={accent} />
          <TokenPanel theme="dark" accent={accent} />
        </div>
      </Section>
      <div className="mt-8 grid gap-4 lg:grid-cols-2">
        <div
          data-theme="light"
          data-accent={accent === 'blue' ? undefined : accent}
          className="rounded-lg border border-border bg-bg p-4 text-text"
        >
          <Gallery />
        </div>
        <div
          data-theme="dark"
          data-accent={accent === 'blue' ? undefined : accent}
          className="rounded-lg border border-border bg-bg p-4 text-text"
        >
          <Gallery />
        </div>
      </div>
    </div>
  )
}
