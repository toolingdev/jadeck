import * as RSlider from "@radix-ui/react-slider";
import * as RSwitch from "@radix-ui/react-switch";
import * as RDialog from "@radix-ui/react-dialog";
import { X } from "lucide-react";
import { forwardRef, type ButtonHTMLAttributes, type HTMLAttributes, type InputHTMLAttributes, type ReactNode } from "react";
import { cn } from "@/lib/cn";

// ---------------------------------------------------------------- Button

type Variant = "default" | "accent" | "ghost" | "danger" | "pgm" | "pvw" | "outline";
type Size = "sm" | "md" | "lg" | "xl" | "icon";

const variants: Record<Variant, string> = {
  default: "bg-raise text-fg border border-line hover:bg-line-strong/70 active:bg-line-strong",
  accent: "bg-accent text-accent-fg font-semibold hover:brightness-110 active:brightness-95",
  ghost: "text-muted hover:text-fg hover:bg-raise",
  danger: "bg-pgm/15 text-bad border border-pgm/40 hover:bg-pgm/25",
  pgm: "bg-pgm text-white font-semibold hover:brightness-110",
  pvw: "bg-pvw text-[#04130a] font-semibold hover:brightness-110",
  outline: "border border-line-strong text-fg hover:bg-raise",
};
const sizes: Record<Size, string> = {
  sm: "h-8 px-3 text-xs rounded-lg gap-1.5",
  md: "h-10 px-4 text-sm rounded-xl gap-2",
  lg: "h-12 px-5 text-base rounded-xl gap-2",
  xl: "h-16 px-6 text-lg rounded-2xl gap-3",
  icon: "h-10 w-10 rounded-xl",
};

export const Button = forwardRef<HTMLButtonElement, ButtonHTMLAttributes<HTMLButtonElement> & { variant?: Variant; size?: Size }>(
  ({ className, variant = "default", size = "md", type = "button", ...p }, ref) => (
    <button
      ref={ref}
      type={type}
      className={cn(
        "inline-flex items-center justify-center whitespace-nowrap transition select-none disabled:opacity-40 disabled:pointer-events-none",
        variants[variant],
        sizes[size],
        className,
      )}
      {...p}
    />
  ),
);
Button.displayName = "Button";

// ------------------------------------------------------------------ Card

export function Card({ className, ...p }: HTMLAttributes<HTMLDivElement>) {
  return <section className={cn("rounded-[var(--radius-card)] border border-line bg-panel", className)} {...p} />;
}

export function CardHeader({ title, meta, children, className }: { title: ReactNode; meta?: ReactNode; children?: ReactNode; className?: string }) {
  return (
    <header className={cn("flex flex-wrap items-center gap-x-3 gap-y-1 px-4 pt-3.5 pb-2", className)}>
      <h2 className="shrink-0 text-[13px] font-semibold tracking-wide text-fg">{title}</h2>
      {/* phones: the note goes under the title instead of squeezing beside it */}
      {meta && <span className="text-[11px] uppercase tracking-[0.08em] text-dim max-sm:order-3 max-sm:basis-full">{meta}</span>}
      <div className="ml-auto flex shrink-0 items-center gap-2">{children}</div>
    </header>
  );
}

export function CardBody({ className, ...p }: HTMLAttributes<HTMLDivElement>) {
  return <div className={cn("px-4 pb-4", className)} {...p} />;
}

// ----------------------------------------------------------------- Badge

type Tone = "neutral" | "good" | "bad" | "warn" | "info" | "pgm" | "pvw" | "accent";
const tones: Record<Tone, string> = {
  neutral: "bg-raise text-muted border-line",
  good: "bg-good/10 text-good border-good/30",
  bad: "bg-bad/10 text-bad border-bad/30",
  warn: "bg-warn/10 text-warn border-warn/30",
  info: "bg-info/10 text-info border-info/30",
  pgm: "bg-pgm text-white border-pgm",
  pvw: "bg-pvw text-[#04130a] border-pvw",
  accent: "bg-accent/15 text-accent border-accent/40",
};

export function Badge({ tone = "neutral", className, children, dot }: { tone?: Tone; className?: string; children: ReactNode; dot?: boolean }) {
  return (
    <span className={cn("inline-flex items-center gap-1.5 rounded-md border px-1.5 py-0.5 text-[10.5px] font-semibold uppercase tracking-[0.07em]", tones[tone], className)}>
      {dot && <span className="h-1.5 w-1.5 rounded-full bg-current" />}
      {children}
    </span>
  );
}

export function Dot({ on, className }: { on: boolean | null | undefined; className?: string }) {
  return (
    <span
      className={cn(
        "inline-block h-2 w-2 rounded-full",
        on ? "bg-good shadow-[0_0_0_3px_rgba(74,222,128,0.15)]" : on === false ? "bg-bad" : "bg-dim",
        className,
      )}
    />
  );
}

// ---------------------------------------------------------------- Slider

export function Slider({ value, min, max, step = 1, onChange, onCommit, className, label }: {
  value: number; min: number; max: number; step?: number; label?: string;
  onChange?: (v: number) => void; onCommit?: (v: number) => void; className?: string;
}) {
  return (
    <RSlider.Root
      className={cn("relative flex h-8 w-full touch-none select-none items-center", className)}
      value={[value]} min={min} max={max} step={step}
      onValueChange={(v) => onChange?.(v[0])}
      onValueCommit={(v) => onCommit?.(v[0])}
      aria-label={label}
    >
      <RSlider.Track className="relative h-1.5 grow rounded-full bg-raise">
        <RSlider.Range className="absolute h-full rounded-full bg-accent" />
      </RSlider.Track>
      <RSlider.Thumb className="block h-5 w-5 rounded-full border-2 border-accent bg-bg shadow focus:outline-none focus-visible:ring-4 focus-visible:ring-accent/30" />
    </RSlider.Root>
  );
}

// ---------------------------------------------------------------- Switch

export function Switch({ checked, onChange, disabled, label }: { checked: boolean; onChange: (v: boolean) => void; disabled?: boolean; label?: string }) {
  return (
    <RSwitch.Root
      checked={checked} onCheckedChange={onChange} disabled={disabled} aria-label={label}
      className="relative h-6 w-11 shrink-0 rounded-full border border-line-strong bg-raise transition data-[state=checked]:border-accent data-[state=checked]:bg-accent disabled:opacity-40"
    >
      <RSwitch.Thumb className="block h-4.5 w-4.5 translate-x-0.5 rounded-full bg-fg shadow transition data-[state=checked]:translate-x-[22px] data-[state=checked]:bg-accent-fg" />
    </RSwitch.Root>
  );
}

// ------------------------------------------------------------- Segmented

export function Segmented<T extends string>({ value, options, onChange, className }: {
  value: T; options: { value: T; label: ReactNode }[]; onChange: (v: T) => void; className?: string;
}) {
  return (
    <div className={cn("inline-flex rounded-xl border border-line bg-bg p-0.5", className)} role="tablist">
      {options.map((o) => (
        <button
          key={o.value} type="button" role="tab" aria-selected={o.value === value}
          onClick={() => onChange(o.value)}
          className={cn(
            "h-8 rounded-[10px] px-3 text-xs font-semibold transition",
            o.value === value ? "bg-raise text-fg shadow-sm" : "text-muted hover:text-fg",
          )}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

// ----------------------------------------------------------------- Input

export const Input = forwardRef<HTMLInputElement, InputHTMLAttributes<HTMLInputElement>>(({ className, ...p }, ref) => (
  <input
    ref={ref}
    className={cn("h-10 w-full rounded-xl border border-line bg-bg px-3 text-sm text-fg placeholder:text-dim focus:border-accent focus:outline-none", className)}
    {...p}
  />
));
Input.displayName = "Input";

export function Select({ value, onChange, options, className }: {
  value: string; onChange: (v: string) => void; options: { value: string; label: string }[]; className?: string;
}) {
  return (
    <select
      value={value} onChange={(e) => onChange(e.target.value)}
      className={cn("h-10 w-full rounded-xl border border-line bg-bg px-3 text-sm text-fg focus:border-accent focus:outline-none", className)}
    >
      {options.map((o) => (
        <option key={o.value} value={o.value}>{o.label}</option>
      ))}
    </select>
  );
}

// ---------------------------------------------------------------- Dialog

export function Dialog({ open, onOpenChange, title, description, children, wide }: {
  open: boolean; onOpenChange: (v: boolean) => void; title: string; description?: string; children: ReactNode; wide?: boolean;
}) {
  return (
    <RDialog.Root open={open} onOpenChange={onOpenChange}>
      <RDialog.Portal>
        <RDialog.Overlay className="fixed inset-0 z-50 bg-black/70 backdrop-blur-sm" />
        <RDialog.Content
          className={cn(
            "fixed left-1/2 top-1/2 z-50 max-h-[90dvh] w-[calc(100vw-2rem)] -translate-x-1/2 -translate-y-1/2 overflow-y-auto rounded-2xl border border-line bg-panel p-5 shadow-2xl",
            wide ? "max-w-2xl" : "max-w-md",
          )}
        >
          <div className="mb-4 flex items-start gap-3">
            <div>
              <RDialog.Title className="text-base font-semibold">{title}</RDialog.Title>
              {description && <RDialog.Description className="mt-1 text-sm text-muted">{description}</RDialog.Description>}
            </div>
            <RDialog.Close asChild>
              <Button variant="ghost" size="icon" className="-mr-2 -mt-2 ml-auto" aria-label="Close"><X size={18} /></Button>
            </RDialog.Close>
          </div>
          {children}
        </RDialog.Content>
      </RDialog.Portal>
    </RDialog.Root>
  );
}

export function Empty({ children }: { children: ReactNode }) {
  return <div className="rounded-xl border border-dashed border-line px-4 py-6 text-center text-sm text-muted">{children}</div>;
}
