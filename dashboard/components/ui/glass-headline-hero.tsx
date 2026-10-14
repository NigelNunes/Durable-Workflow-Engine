"use client";

import * as React from "react";

/* ─── types ─── */

export interface HeroAction {
  label: string;
  href?: string;
  onClick?: () => void;
}

export interface GlassHeadlineHeroProps {
  title: string;
  eyebrow?: string;
  description?: string;
  primaryAction?: HeroAction;
  secondaryAction?: HeroAction;
  colors?: string[];
  height?: string;
  className?: string;
}

/* ─── constants ─── */

export const DEFAULT_COLORS = [
  "#0D0A14",
  "#FF5A1F",
  "#FF9EC1",
  "#2F4CFF",
  "#FFE6B8",
];

const FORM_MS = 1100;
const DOME = 3;
const IDLE_S = 2.5;
const SLOW_FRAME_S = 0.05;
const SLOW_FRAMES = 8;
const CRAWL_FRAME_S = 0.15;

/* ─── helpers (#region glass) ─── */

export function hexToRgb(hex: string): [number, number, number] {
  const h = hex.replace("#", "");
  return [
    parseInt(h.substring(0, 2), 16) / 255,
    parseInt(h.substring(2, 4), 16) / 255,
    parseInt(h.substring(4, 6), 16) / 255,
  ];
}

export function paletteOf(colors: string[]): Float32Array {
  const out = new Float32Array(Math.max(colors.length, 5) * 3);
  for (let i = 0; i < colors.length; i++) {
    const [r, g, b] = hexToRgb(colors[i]);
    out[i * 3] = r;
    out[i * 3 + 1] = g;
    out[i * 3 + 2] = b;
  }
  return out;
}

export function splitWords(text: string): string[] {
  return text.split(/\s+/).filter(Boolean);
}

export function bevelPx(fontPx: number, scale = 1): number {
  return Math.max(2, fontPx * 0.075 * scale);
}

export function formed(t: number, ms = FORM_MS): number {
  const x = Math.min(1, Math.max(0, t / ms));
  return 1 - (1 - x) ** 3;
}

export function fallbackBackground(colors: string[]): string {
  const a = colors[0] ?? "#0D0A14";
  const b = colors[1] ?? "#FF5A1F";
  const c = colors[2] ?? "#FF9EC1";
  const d = colors[3] ?? "#2F4CFF";
  const rgba = (hex: string, alpha: number) => {
    const [r, g, b] = hexToRgb(hex);
    return `rgba(${Math.round(r * 255)},${Math.round(g * 255)},${Math.round(b * 255)},${alpha})`;
  };
  return [
    `radial-gradient(60% 50% at 25% 30%,${rgba(b, 0.4)},transparent 70%)`,
    `radial-gradient(50% 45% at 78% 35%,${rgba(d, 0.4)},transparent 70%)`,
    `radial-gradient(45% 40% at 60% 80%,${rgba(c, 0.27)},transparent 70%)`,
    rgba(a, 1),
  ].join(",");
}

export function follow(
  cur: number,
  tgt: number,
  speed: number,
  dt: number,
): number {
  const d = tgt - cur;
  return Math.abs(d) < 0.0001 ? tgt : cur + d * (1 - Math.exp(-speed * dt));
}

export function orbit(
  t: number,
  rx: number,
  ry: number,
  speed: number,
): [number, number] {
  return [Math.cos(t * speed) * rx, Math.sin(t * speed * 0.92) * ry];
}

/* #endregion */

/* ─── shaders ─── */

const VERT = `#version 300 es
precision highp float;
layout(location = 0) in vec2 a_pos;
out vec2 v_uv;
void main(){
  v_uv = a_pos * 0.5 + 0.5;
  gl_Position = vec4(a_pos, 0.0, 1.0);
}`;

const FIELD = `#version 300 es
precision highp float;
in vec2 v_uv;
out vec4 fragColor;

uniform float u_time;
uniform float u_form;
uniform vec2  u_res;
uniform vec2  u_mouse;
uniform vec3  u_palette[5];
uniform float u_count;
uniform float u_dome;
uniform float u_octaves;

float hash(vec2 p){
  return fract(sin(dot(p, vec2(127.1, 311.7))) * 43758.5453);
}

float noise(vec2 p){
  vec2 i = floor(p);
  vec2 f = fract(p);
  vec2 u = f * f * (3.0 - 2.0 * f);
  return mix(
    mix(hash(i), hash(i + vec2(1.0, 0.0)), u.x),
    mix(hash(i + vec2(0.0, 1.0)), hash(i + vec2(1.0, 1.0)), u.x),
    u.y
  );
}

float fbm(vec2 p){
  float v = 0.0;
  float a = 0.5;
  mat2 turn = mat2(0.8, 0.6, -0.6, 0.8);
  for (int i = 0; i < 5; i++) {
    if (float(i) >= u_octaves) break;
    v += a * noise(p);
    p = turn * p * 2.02;
    a *= 0.5;
  }
  return v;
}

void main(){
  float aspect = u_res.x / max(u_res.y, 1.0);
  vec2 p = vec2(v_uv.x * aspect, v_uv.y) * 1.2;
  float t = u_time * 0.06;
  // Light pointer nudges the field slightly without bleaching the center.
  vec2 mouse = (u_mouse - 0.5) * vec2(aspect, 1.0);
  p += mouse * 0.04;

  vec2 q = vec2(fbm(p + vec2(0.0, t)), fbm(p + vec2(5.2, 1.3) - t));
  vec2 r = vec2(
    fbm(p + 3.5 * q + vec2(1.7, 9.2) + t * 1.4),
    fbm(p + 3.5 * q + vec2(8.3, 2.8) - t * 1.1)
  );
  float f = fbm(p + 3.0 * r);

  // Ground dominates; accents are flowing bands — never a white hot dome.
  vec3 col = u_palette[0];
  col = mix(col, u_palette[3], smoothstep(0.25, 0.72, q.x) * 0.85);
  col = mix(col, u_palette[1], smoothstep(0.34, 0.74, f) * 0.9);
  col = mix(col, u_palette[2], smoothstep(0.42, 0.8, r.y) * 0.75);
  // Cream is edge-only; never fill the focal area behind the headline.
  col = mix(col, u_palette[4], smoothstep(0.62, 0.95, f * r.x * 1.8) * 0.18);

  float bands = 0.5 + 0.5 * sin((f * 7.0 + r.x * 3.0) * 3.14159);
  col *= mix(0.82, 0.98, smoothstep(0.2, 0.8, bands));

  // Tiny warm kiss — no bright center plate.
  vec2 g = (v_uv - vec2(0.5, 0.56)) / vec2(0.55, 0.22);
  col += u_palette[1] * 0.04 * exp(-dot(g, g));

  // Quieter toward the foot; soft dark scrim under description / buttons.
  col *= mix(0.38, 1.0, smoothstep(0.02, 0.62, v_uv.y));
  vec2 s = (v_uv - vec2(0.5, 0.36)) / vec2(0.34, 0.16);
  col *= 1.0 - 0.55 * exp(-dot(s, s));

  // Hard cap so the field cannot bleach toward white.
  float luma = dot(col, vec3(0.299, 0.587, 0.114));
  if (luma > 0.42) {
    col *= 0.42 / luma;
  }

  col = mix(u_palette[0], col, u_form);
  fragColor = vec4(col, 1.0);
}`;

const BLUR = `#version 300 es
precision highp float;
in vec2 v_uv;
out vec4 fragColor;

uniform sampler2D u_tex;
uniform vec2 u_dir;
uniform vec2 u_res;

void main(){
  vec2 texel = 1.0 / u_res;
  vec4 acc = vec4(0.0);
  float w[5];
  w[0] = 0.227027;
  w[1] = 0.1945946;
  w[2] = 0.1216216;
  w[3] = 0.054054;
  w[4] = 0.016216;
  acc += texture(u_tex, v_uv) * w[0];
  for(int i = 1; i < 5; i++){
    vec2 off = u_dir * texel * float(i) * 2.0;
    acc += texture(u_tex, v_uv + off) * w[i];
    acc += texture(u_tex, v_uv - off) * w[i];
  }
  fragColor = acc;
}`;

const DOME_PASS = `#version 300 es
precision highp float;
in vec2 v_uv;
out vec4 fragColor;

uniform sampler2D u_mask;
uniform sampler2D u_bevel;
uniform float u_dome;

void main(){
  float m = texture(u_mask, v_uv).a;
  float bevel = texture(u_bevel, v_uv).r;
  float interior = smoothstep(0.08, 0.55, m);
  float dome = interior * u_dome * 0.22;
  dome *= 1.0 - smoothstep(0.0, 0.85, bevel);
  fragColor = vec4(bevel, 0.0, dome, m);
}`;

const GLASS = `#version 300 es
precision highp float;
in vec2 v_uv;
out vec4 fragColor;

uniform sampler2D u_field;
uniform sampler2D u_height;
uniform vec2 u_res;
uniform vec2 u_light;
uniform float u_form;
uniform float u_glass;
uniform float u_lite;

float heightAt(vec2 uv){
  vec4 h = texture(u_height, uv);
  return h.r + h.b;
}

vec3 sampleField(vec2 uv, vec2 refractOff){
  float ab = mix(0.0045, 0.0015, u_lite);
  vec3 base = texture(u_field, uv + refractOff).rgb;
  float r = texture(u_field, uv + refractOff + vec2(ab, 0.0)).r;
  float b = texture(u_field, uv + refractOff - vec2(ab, 0.0)).b;
  return vec3(r, base.g, b);
}

void main(){
  vec4 hm = texture(u_height, v_uv);
  float mask = hm.a;
  float h = hm.r + hm.b;

  vec3 bg = texture(u_field, v_uv).rgb;

  if(mask < 0.02){
    fragColor = vec4(bg, 1.0);
    return;
  }

  vec2 px = 1.0 / u_res;
  float hL = heightAt(v_uv - vec2(px.x, 0.0));
  float hR = heightAt(v_uv + vec2(px.x, 0.0));
  float hD = heightAt(v_uv - vec2(0.0, px.y));
  float hU = heightAt(v_uv + vec2(0.0, px.y));

  vec3 n = normalize(vec3(hL - hR, hD - hU, mix(0.14, 0.22, u_lite)));
  vec2 refr = n.xy * (0.035 + hm.r * 0.08) * u_form;

  vec3 col = sampleField(v_uv, refr);

  vec2 asp = vec2(u_res.x / u_res.y, 1.0);
  vec3 L = normalize(vec3((u_light - v_uv) * asp, 0.35));
  float spec = pow(max(dot(reflect(-L, n), vec3(0.0, 0.0, 1.0)), 0.0), mix(28.0, 18.0, u_lite));
  col += spec * vec3(1.0, 0.98, 0.94) * 0.38;

  float rim = smoothstep(0.15, 0.65, hm.r);
  col += rim * vec3(0.85, 0.92, 1.0) * 0.08;

  float contact = smoothstep(0.55, 0.08, mask) * smoothstep(0.0, 0.35, hm.r);
  col *= 1.0 - contact * 0.35;

  col = mix(bg, col, clamp(mask * 1.15, 0.0, 1.0) * u_glass * u_form);
  fragColor = vec4(col, 1.0);
}`;

/* ─── CSS ─── */

const CSS = `
.ghr-root {
  position: relative;
  overflow: hidden;
  width: 100%;
  isolation: isolate;
}

.ghr-canvas {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  display: block;
  z-index: 0;
}

.ghr-content {
  position: relative;
  z-index: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 18px;
  height: 100%;
  padding: clamp(24px, 6vw, 72px) 24px;
  text-align: center;
  box-sizing: border-box;
  pointer-events: none;
}

.ghr-content a,
.ghr-content button {
  pointer-events: auto;
}

.ghr-eyebrow {
  display: inline-flex;
  align-items: center;
  height: 30px;
  padding: 0 14px;
  border-radius: 999px;
  font-family: var(--font-sans), system-ui, sans-serif;
  font-size: 12px;
  font-weight: 600;
  letter-spacing: 0.12em;
  text-transform: uppercase;
  color: rgba(255,255,255,0.84);
  border: 1px solid rgba(255,255,255,0.2);
  background: rgba(255,255,255,0.06);
  -webkit-backdrop-filter: blur(10px);
  backdrop-filter: blur(10px);
  opacity: 0;
}

.ghr-root[data-glass="true"] .ghr-eyebrow,
.ghr-root[data-fallback="true"] .ghr-eyebrow {
  animation: ghr-fade 0.7s 0.05s forwards;
}

.ghr-title {
  font-family: var(--font-display), var(--font-sans), system-ui, sans-serif;
  font-size: clamp(3.2rem, 9vw, 7.5rem);
  font-weight: 800;
  line-height: 0.94;
  letter-spacing: -0.045em;
  /* Stay invisible until glass is ready — avoids a solid-white flash. */
  color: transparent;
  -webkit-text-fill-color: transparent;
  margin: 0;
  max-width: 12ch;
  text-wrap: balance;
}

.ghr-root[data-fallback="true"]:not([data-glass="true"]) .ghr-title {
  color: rgba(255,255,255,0.96);
  -webkit-text-fill-color: rgba(255,255,255,0.96);
}

.ghr-word {
  display: inline-block;
  opacity: 0;
  transform: translateY(0.28em);
}

.ghr-root[data-glass="true"] .ghr-word,
.ghr-root[data-fallback="true"] .ghr-word {
  animation: ghr-word-in 0.65s forwards;
}

@keyframes ghr-word-in {
  to {
    opacity: 1;
    transform: translateY(0);
  }
}

.ghr-desc {
  font-family: var(--font-sans), system-ui, sans-serif;
  font-size: clamp(1rem, 1.8vw, 1.25rem);
  color: rgba(255,255,255,0.78);
  margin: 0;
  max-width: 38rem;
  line-height: 1.55;
  opacity: 0;
}

.ghr-root[data-glass="true"] .ghr-desc,
.ghr-root[data-fallback="true"] .ghr-desc {
  animation: ghr-fade 0.8s 0.12s forwards;
}

@keyframes ghr-fade {
  to { opacity: 1; }
}

.ghr-actions {
  display: flex;
  gap: 0.75rem;
  flex-wrap: wrap;
  justify-content: center;
  opacity: 0;
}

.ghr-root[data-glass="true"] .ghr-actions,
.ghr-root[data-fallback="true"] .ghr-actions {
  animation: ghr-fade 0.8s 0.22s forwards;
}

.ghr-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 0.45rem;
  padding: 0.75rem 1.75rem;
  border-radius: 9999px;
  font-size: 0.95rem;
  font-weight: 600;
  cursor: pointer;
  text-decoration: none;
  border: none;
  transition: transform 0.15s, box-shadow 0.15s, background 0.15s;
  line-height: 1.4;
  font-family: var(--font-sans), system-ui, sans-serif;
}

.ghr-btn:hover {
  transform: translateY(-1px);
}

.ghr-btn:active {
  transform: translateY(0);
}

.ghr-primary {
  background: #fff;
  color: #0d0a14;
  box-shadow: 0 0 0 1px rgba(255,255,255,0.15), 0 4px 20px rgba(0,0,0,0.3);
}

.ghr-primary:hover {
  box-shadow: 0 0 0 1px rgba(255,255,255,0.3), 0 8px 30px rgba(0,0,0,0.4);
}

.ghr-secondary {
  background: rgba(255,255,255,0.1);
  color: #fff;
  box-shadow: 0 0 0 1px rgba(255,255,255,0.15);
  backdrop-filter: blur(8px);
  -webkit-backdrop-filter: blur(8px);
}

.ghr-secondary:hover {
  background: rgba(255,255,255,0.18);
  box-shadow: 0 0 0 1px rgba(255,255,255,0.3);
}

.ghr-arrow {
  display: inline-block;
  transition: transform 0.15s ease;
}

.ghr-primary:hover .ghr-arrow {
  transform: translateX(3px);
}
`;

/* ─── WebGL helpers ─── */

interface Prog {
  program: WebGLProgram;
  locs: Record<string, WebGLUniformLocation | null>;
}

function compileProg(
  gl: WebGL2RenderingContext,
  vs: string,
  fs: string,
  uniforms: string[],
): Prog {
  const compile = (src: string, type: number) => {
    const s = gl.createShader(type)!;
    gl.shaderSource(s, src);
    gl.compileShader(s);
    if (!gl.getShaderParameter(s, gl.COMPILE_STATUS)) {
      const info = gl.getShaderInfoLog(s);
      gl.deleteShader(s);
      throw new Error(`Shader compile: ${info}`);
    }
    return s;
  };

  const vShader = compile(vs, gl.VERTEX_SHADER);
  const fShader = compile(fs, gl.FRAGMENT_SHADER);
  const program = gl.createProgram()!;
  gl.attachShader(program, vShader);
  gl.attachShader(program, fShader);
  gl.linkProgram(program);
  if (!gl.getProgramParameter(program, gl.LINK_STATUS)) {
    const info = gl.getProgramInfoLog(program);
    gl.deleteProgram(program);
    throw new Error(`Program link: ${info}`);
  }
  gl.deleteShader(vShader);
  gl.deleteShader(fShader);

  const locs: Record<string, WebGLUniformLocation | null> = {};
  for (const u of uniforms) {
    locs[u] = gl.getUniformLocation(program, u);
  }
  return { program, locs };
}

function makeFBO(
  gl: WebGL2RenderingContext,
  w: number,
  h: number,
  precise = true,
): { fb: WebGLFramebuffer; tex: WebGLTexture } {
  const tex = gl.createTexture()!;
  gl.bindTexture(gl.TEXTURE_2D, tex);
  if (precise) {
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA16F, w, h, 0, gl.RGBA, gl.HALF_FLOAT, null);
  } else {
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, w, h, 0, gl.RGBA, gl.UNSIGNED_BYTE, null);
  }
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);

  const fb = gl.createFramebuffer()!;
  gl.bindFramebuffer(gl.FRAMEBUFFER, fb);
  gl.framebufferTexture2D(
    gl.FRAMEBUFFER,
    gl.COLOR_ATTACHMENT0,
    gl.TEXTURE_2D,
    tex,
    0,
  );
  gl.bindFramebuffer(gl.FRAMEBUFFER, null);
  return { fb, tex };
}

function buildMask(
  root: HTMLElement,
  canvas: HTMLCanvasElement,
  maskCanvas: HTMLCanvasElement,
  ctx2d: CanvasRenderingContext2D,
): boolean {
  const dpr = canvas.width / Math.max(1, canvas.clientWidth);
  const rootRect = root.getBoundingClientRect();
  const cw = canvas.width;
  const ch = canvas.height;

  maskCanvas.width = cw;
  maskCanvas.height = ch;
  ctx2d.setTransform(1, 0, 0, 1, 0, 0);
  ctx2d.clearRect(0, 0, cw, ch);

  const words = root.querySelectorAll<HTMLElement>(".ghr-word");
  if (!words.length) return false;

  let drew = false;
  words.forEach((el) => {
    const style = window.getComputedStyle(el);
    const rect = el.getBoundingClientRect();
    const x = (rect.left - rootRect.left) * dpr;
    const y = (rect.top - rootRect.top) * dpr;
    const fontSize = parseFloat(style.fontSize) * dpr;
    const fontWeight = style.fontWeight;
    const fontFamily = style.fontFamily;
    const letterSpacing = parseFloat(style.letterSpacing) || 0;

    ctx2d.save();
    ctx2d.font = `${fontWeight} ${fontSize}px ${fontFamily}`;
    ctx2d.fillStyle = "#fff";
    ctx2d.textBaseline = "top";
    (ctx2d as CanvasRenderingContext2D & { letterSpacing?: string }).letterSpacing =
      `${letterSpacing * dpr}px`;
    ctx2d.fillText(el.textContent ?? "", x, y);
    ctx2d.restore();
    drew = true;
  });

  return drew;
}

/* ─── Action ─── */

function Action({
  action,
  variant,
}: {
  action: HeroAction;
  variant: "primary" | "secondary";
}) {
  const cls = `ghr-btn ${variant === "primary" ? "ghr-primary" : "ghr-secondary"}`;
  const inner = (
    <>
      {action.label}
      {variant === "primary" ? <span className="ghr-arrow" aria-hidden>→</span> : null}
    </>
  );
  if (action.href) {
    return (
      <a href={action.href} className={cls}>
        {inner}
      </a>
    );
  }
  return (
    <button type="button" className={cls} onClick={action.onClick}>
      {inner}
    </button>
  );
}

/* ─── Component ─── */

export default function GlassHeadlineHero({
  title,
  eyebrow,
  description,
  primaryAction,
  secondaryAction,
  colors = DEFAULT_COLORS,
  height = "100svh",
  className,
}: GlassHeadlineHeroProps) {
  const rootRef = React.useRef<HTMLDivElement>(null);
  const canvasRef = React.useRef<HTMLCanvasElement>(null);
  const rafRef = React.useRef(0);
  const visibleRef = React.useRef(true);
  const mouseRef = React.useRef({ x: 0.5, y: 0.5 });
  const smoothLight = React.useRef({ x: 0.35, y: 0.35 });
  const [glassOn, setGlassOn] = React.useState(false);
  const [fallback, setFallback] = React.useState(false);
  const [glEpoch, setGlEpoch] = React.useState(0);
  const glassFlagRef = React.useRef(false);
  // Live palette — theme/color changes must not tear down the GL context.
  const liveRef = React.useRef({ palette: paletteOf(colors), count: colors.length });
  const words = splitWords(title);

  React.useEffect(() => {
    liveRef.current = { palette: paletteOf(colors), count: colors.length };
  }, [colors]);

  React.useEffect(() => {
    const root = rootRef.current;
    const canvas = canvasRef.current;
    if (!root || !canvas) return;

    let disposed = false;
    glassFlagRef.current = false;
    setGlassOn(false);
    setFallback(false);

    const failSoft = () => {
      // Never override a working glass pass with solid white.
      if (!disposed && !glassFlagRef.current) setFallback(true);
    };
    // If glass never forms, show solid type instead of invisible headline forever.
    const fallbackTimer = window.setTimeout(failSoft, 2800);

    const lite =
      window.matchMedia("(prefers-reduced-motion: reduce)").matches ||
      window.matchMedia("(hover: none)").matches ||
      (navigator.hardwareConcurrency ?? 8) <= 4;

    const maskCanvas = document.createElement("canvas");
    const ctx2d = maskCanvas.getContext("2d", { alpha: true });
    if (!ctx2d) {
      failSoft();
      return () => {
        disposed = true;
        window.clearTimeout(fallbackTimer);
      };
    }

    const gl = canvas.getContext("webgl2", {
      alpha: false,
      antialias: false,
      powerPreference: lite ? "low-power" : "high-performance",
    });

    if (!gl) {
      failSoft();
      return () => {
        disposed = true;
        window.clearTimeout(fallbackTimer);
      };
    }

    const quadBuf = gl.createBuffer()!;
    gl.bindBuffer(gl.ARRAY_BUFFER, quadBuf);
    gl.bufferData(
      gl.ARRAY_BUFFER,
      new Float32Array([-1, -1, 1, -1, -1, 1, 1, 1]),
      gl.STATIC_DRAW,
    );

    const vao = gl.createVertexArray()!;
    gl.bindVertexArray(vao);
    gl.enableVertexAttribArray(0);
    gl.vertexAttribPointer(0, 2, gl.FLOAT, false, 0, 0);

    let P: Record<"field" | "blur" | "dome" | "glass", Prog> | null = null;
    try {
      P = {
        field: compileProg(gl, VERT, FIELD, [
          "u_time",
          "u_form",
          "u_res",
          "u_mouse",
          "u_palette",
          "u_count",
          "u_dome",
          "u_octaves",
        ]),
        blur: compileProg(gl, VERT, BLUR, ["u_tex", "u_dir", "u_res"]),
        dome: compileProg(gl, VERT, DOME_PASS, ["u_mask", "u_bevel", "u_dome"]),
        glass: compileProg(gl, VERT, GLASS, [
          "u_field",
          "u_height",
          "u_res",
          "u_light",
          "u_form",
          "u_glass",
          "u_lite",
        ]),
      };
    } catch {
      failSoft();
      return () => {
        disposed = true;
        window.clearTimeout(fallbackTimer);
      };
    }

    const maskTex = gl.createTexture()!;
    gl.bindTexture(gl.TEXTURE_2D, maskTex);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);

    let w = 0;
    let h = 0;
    let fboField: { fb: WebGLFramebuffer; tex: WebGLTexture } | null = null;
    let fboA: { fb: WebGLFramebuffer; tex: WebGLTexture } | null = null;
    let fboB: { fb: WebGLFramebuffer; tex: WebGLTexture } | null = null;
    let fboHeight: { fb: WebGLFramebuffer; tex: WebGLTexture } | null = null;
    let maskReady = false;
    let glassFade = 0;

    const uploadMask = () => {
      if (!buildMask(root, canvas, maskCanvas, ctx2d)) return;
      gl.bindTexture(gl.TEXTURE_2D, maskTex);
      gl.pixelStorei(gl.UNPACK_FLIP_Y_WEBGL, true);
      gl.texImage2D(
        gl.TEXTURE_2D,
        0,
        gl.RGBA,
        gl.RGBA,
        gl.UNSIGNED_BYTE,
        maskCanvas,
      );
      gl.pixelStorei(gl.UNPACK_FLIP_Y_WEBGL, false);
      maskReady = true;
    };

    const resize = () => {
      const dpr = Math.min(window.devicePixelRatio, lite ? 1.25 : 2);
      const cw = canvas.clientWidth;
      const ch = canvas.clientHeight;
      const nw = Math.max(1, Math.round(cw * dpr));
      const nh = Math.max(1, Math.round(ch * dpr));
      if (nw === w && nh === h) return;
      w = nw;
      h = nh;
      canvas.width = w;
      canvas.height = h;

      const dispose = (f: { fb: WebGLFramebuffer; tex: WebGLTexture } | null) => {
        if (!f) return;
        gl.deleteTexture(f.tex);
        gl.deleteFramebuffer(f.fb);
      };
      dispose(fboField);
      dispose(fboA);
      dispose(fboB);
      dispose(fboHeight);

      // 8-bit targets only — float FBOs can go incomplete and lose the context mid-run.
      fboField = makeFBO(gl, w, h, false);
      fboA = makeFBO(gl, w, h, false);
      fboB = makeFBO(gl, w, h, false);
      fboHeight = makeFBO(gl, w, h, false);

      requestAnimationFrame(uploadMask);
    };

    resize();

    const t0 = performance.now();
    let lastFrame = t0;
    let slowCount = 0;
    let crawling = false;
    let running = true;

    const draw = () => {
      gl.bindVertexArray(vao);
      gl.drawArrays(gl.TRIANGLE_STRIP, 0, 4);
    };

    const runHeightPass = () => {
      if (!P || !fboA || !fboB || !fboHeight || !maskReady) return;

      gl.bindFramebuffer(gl.FRAMEBUFFER, fboA.fb);
      gl.viewport(0, 0, w, h);
      gl.useProgram(P.blur.program);
      gl.activeTexture(gl.TEXTURE0);
      gl.bindTexture(gl.TEXTURE_2D, maskTex);
      gl.uniform1i(P.blur.locs.u_tex!, 0);
      gl.uniform2f(P.blur.locs.u_dir!, 1, 0);
      gl.uniform2f(P.blur.locs.u_res!, w, h);
      draw();

      gl.bindFramebuffer(gl.FRAMEBUFFER, fboB.fb);
      gl.activeTexture(gl.TEXTURE0);
      gl.bindTexture(gl.TEXTURE_2D, fboA.tex);
      gl.uniform1i(P.blur.locs.u_tex!, 0);
      gl.uniform2f(P.blur.locs.u_dir!, 0, 1);
      gl.uniform2f(P.blur.locs.u_res!, w, h);
      draw();

      gl.bindFramebuffer(gl.FRAMEBUFFER, fboHeight.fb);
      gl.useProgram(P.dome.program);
      gl.activeTexture(gl.TEXTURE0);
      gl.bindTexture(gl.TEXTURE_2D, maskTex);
      gl.uniform1i(P.dome.locs.u_mask!, 0);
      gl.activeTexture(gl.TEXTURE1);
      gl.bindTexture(gl.TEXTURE_2D, fboB.tex);
      gl.uniform1i(P.dome.locs.u_bevel!, 1);
      gl.uniform1f(P.dome.locs.u_dome!, DOME);
      draw();
    };

    const loop = (now: number) => {
      if (!running) return;
      rafRef.current = requestAnimationFrame(loop);

      if (!visibleRef.current) return;

      resize();
      if (!P || !fboField || !fboHeight) return;

      const dt = Math.min((now - lastFrame) / 1000, 0.1);
      lastFrame = now;

      if (dt > SLOW_FRAME_S) {
        slowCount++;
        if (slowCount > SLOW_FRAMES) crawling = true;
      } else {
        slowCount = Math.max(0, slowCount - 1);
        if (slowCount === 0) crawling = false;
      }

      if (crawling && dt > CRAWL_FRAME_S) return;

      const elapsed = (now - t0) / 1000;
      const formT = formed(now - t0);

      const sm = smoothLight.current;
      const mx = mouseRef.current;
      sm.x = follow(sm.x, mx.x, 5.5, dt);
      sm.y = follow(sm.y, mx.y, 5.5, dt);

      const idle =
        elapsed < IDLE_S ? 0 : Math.min(1, (elapsed - IDLE_S) / 2.2);
      const [ox, oy] = orbit(elapsed, 0.022, 0.012, 0.32);
      const lightX = sm.x + ox * idle;
      const lightY = sm.y + oy * idle;

      if (maskReady) {
        runHeightPass();
        glassFade = follow(glassFade, 1, 3.2, dt);
        // Reveal as soon as the mask exists — title stays transparent, no white flash.
        if (!glassFlagRef.current) {
          glassFlagRef.current = true;
          window.clearTimeout(fallbackTimer);
          setFallback(false);
          setGlassOn(true);
        }
      }

      gl.bindFramebuffer(gl.FRAMEBUFFER, fboField.fb);
      gl.viewport(0, 0, w, h);
      gl.useProgram(P.field.program);
      gl.uniform1f(P.field.locs.u_time!, elapsed);
      gl.uniform1f(P.field.locs.u_form!, formT);
      gl.uniform2f(P.field.locs.u_res!, w, h);
      gl.uniform2f(P.field.locs.u_mouse!, lightX, lightY);
      gl.uniform3fv(P.field.locs.u_palette!, liveRef.current.palette);
      gl.uniform1f(P.field.locs.u_count!, liveRef.current.count);
      gl.uniform1f(P.field.locs.u_dome!, DOME);
      gl.uniform1f(P.field.locs.u_octaves!, crawling || lite ? 3.0 : 5.0);
      draw();

      gl.bindFramebuffer(gl.FRAMEBUFFER, null);
      gl.viewport(0, 0, w, h);
      gl.useProgram(P.glass.program);
      gl.activeTexture(gl.TEXTURE0);
      gl.bindTexture(gl.TEXTURE_2D, fboField.tex);
      gl.uniform1i(P.glass.locs.u_field!, 0);
      gl.activeTexture(gl.TEXTURE1);
      gl.bindTexture(gl.TEXTURE_2D, fboHeight.tex);
      gl.uniform1i(P.glass.locs.u_height!, 1);
      gl.uniform2f(P.glass.locs.u_res!, w, h);
      gl.uniform2f(P.glass.locs.u_light!, lightX, lightY);
      gl.uniform1f(P.glass.locs.u_form!, formT);
      gl.uniform1f(P.glass.locs.u_glass!, glassFade);
      gl.uniform1f(P.glass.locs.u_lite!, lite ? 1 : 0);
      draw();
    };

    rafRef.current = requestAnimationFrame(loop);

    const onMove = (e: PointerEvent) => {
      const rect = root.getBoundingClientRect();
      mouseRef.current = {
        x: (e.clientX - rect.left) / rect.width,
        y: (e.clientY - rect.top) / rect.height,
      };
    };

    const onResize = () => {
      resize();
    };

    const io = new IntersectionObserver(
      (entries) => {
        visibleRef.current = entries.some((e) => e.isIntersecting);
      },
      { threshold: 0.05 },
    );
    io.observe(root);

    const onLost = (e: Event) => {
      e.preventDefault();
      running = false;
      cancelAnimationFrame(rafRef.current);
      glassFlagRef.current = false;
      setGlassOn(false);
      glassFade = 0;
    };

    const onRestored = () => {
      glassFlagRef.current = false;
      setGlassOn(false);
      setGlEpoch((k) => k + 1);
    };

    canvas.addEventListener("webglcontextlost", onLost);
    canvas.addEventListener("webglcontextrestored", onRestored);
    window.addEventListener("pointermove", onMove);
    window.addEventListener("resize", onResize);

    const fontReady = document.fonts?.ready;
    if (fontReady) {
      fontReady.then(() => uploadMask()).catch(() => uploadMask());
    } else {
      uploadMask();
    }

    const maskTimer = window.setTimeout(uploadMask, FORM_MS);

    return () => {
      disposed = true;
      running = false;
      cancelAnimationFrame(rafRef.current);
      window.clearTimeout(maskTimer);
      window.clearTimeout(fallbackTimer);
      window.removeEventListener("pointermove", onMove);
      window.removeEventListener("resize", onResize);
      canvas.removeEventListener("webglcontextlost", onLost);
      canvas.removeEventListener("webglcontextrestored", onRestored);
      io.disconnect();

      if (P) {
        for (const p of Object.values(P)) gl.deleteProgram(p.program);
      }
      gl.deleteTexture(maskTex);
      gl.deleteVertexArray(vao);
      gl.deleteBuffer(quadBuf);

      const dispose = (f: { fb: WebGLFramebuffer; tex: WebGLTexture } | null) => {
        if (!f) return;
        gl.deleteTexture(f.tex);
        gl.deleteFramebuffer(f.fb);
      };
      dispose(fboField);
      dispose(fboA);
      dispose(fboB);
      dispose(fboHeight);
    };
  }, [glEpoch]);

  return (
    <>
      <style>{CSS}</style>
      <div
        ref={rootRef}
        className={`ghr-root${className ? ` ${className}` : ""}`}
        data-glass={glassOn ? "true" : undefined}
        data-fallback={fallback && !glassOn ? "true" : undefined}
        style={{ height, background: fallbackBackground(colors) }}
      >
        <canvas ref={canvasRef} className="ghr-canvas" aria-hidden />
        <div className="ghr-content">
          {eyebrow ? <span className="ghr-eyebrow">{eyebrow}</span> : null}
          <h1 className="ghr-title">
            {words.map((word, i) => (
              <React.Fragment key={`${word}-${i}`}>
                <span
                  className="ghr-word"
                  style={{ animationDelay: `${0.1 + i * 0.08}s` }}
                >
                  {word}
                </span>
                {i < words.length - 1 ? " " : null}
              </React.Fragment>
            ))}
          </h1>
          {description ? <p className="ghr-desc">{description}</p> : null}
          {primaryAction || secondaryAction ? (
            <div className="ghr-actions">
              {primaryAction ? (
                <Action action={primaryAction} variant="primary" />
              ) : null}
              {secondaryAction ? (
                <Action action={secondaryAction} variant="secondary" />
              ) : null}
            </div>
          ) : null}
        </div>
      </div>
    </>
  );
}
