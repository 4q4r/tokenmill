import type { Plugin } from "@opencode-ai/plugin"

const ISO_DATE = /^\d{4}-\d{2}-\d{2}$/
const ISO_TIMESTAMP =
  /^\d{4}-\d{2}-\d{2}(?:[T ]\d{2}:\d{2}(?::\d{2}(?:\.\d{1,9})?)?(?:Z|[+-]\d{2}:?\d{2})?)$/
const CLOCK_TIME = /^\d{2}:\d{2}(?::\d{2}(?:\.\d{1,9})?)?(?:Z|[+-]\d{2}:?\d{2})?$/
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i
const REQUEST_ID_VALUE = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/
const DATE_KEYS = new Set(["date", "time", "timestamp", "createdat", "updatedat"])
const REQUEST_ID_KEYS = new Set(["requestid", "correlationid", "traceid", "spanid", "uuid"])
const FNV_OFFSET = 0xcbf29ce484222325n
const FNV_PRIME = 0x100000001b3n

export type StablePrefixResult = {
  content: string
  changed: boolean
  movedLines: string[]
}

export type StableSystemResult = {
  blocks: string[]
  changed: boolean
  movedBlocks: string[]
}

type LineRecord = {
  text: string
  ending: string
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null
}

function unchangedPrefixResult(content: string): StablePrefixResult {
  return { content, changed: false, movedLines: [] }
}

function withoutCarriageReturn(line: string): string {
  return line.endsWith("\r") ? line.slice(0, -1) : line
}

function isFenceDelimiter(line: string): boolean {
  return /^ {0,3}(?:```|~~~)/.test(withoutCarriageReturn(line))
}

function splitLinesPreservingEndings(content: string): LineRecord[] {
  const segments = content.split(/(\r\n|\n)/)
  const lines: LineRecord[] = []
  for (let index = 0; index < segments.length; index += 2) {
    lines.push({ text: segments[index] ?? "", ending: segments[index + 1] ?? "" })
  }
  return lines
}

function normalizedMetadataKey(key: string): string {
  return key.toLowerCase().replace(/[ _-]/g, "")
}

/** Return true only for a whole line containing narrowly recognized metadata. */
function isStandaloneMetadataLine(line: string): boolean {
  const raw = withoutCarriageReturn(line)
  if (/^(?:\t| {4})/.test(raw) || isFenceDelimiter(raw)) return false

  const trimmed = raw.trim()
  if (!trimmed) return false
  if (ISO_DATE.test(trimmed) || ISO_TIMESTAMP.test(trimmed) || UUID.test(trimmed)) return true

  const match = /^([A-Za-z][A-Za-z0-9 _-]{0,31})\s*[:=]\s*(\S+)$/.exec(trimmed)
  if (!match) return false

  const rawKey = match[1]
  const value = match[2]
  if (!rawKey || !value) return false
  const key = normalizedMetadataKey(rawKey)
  if (DATE_KEYS.has(key)) return ISO_DATE.test(value) || ISO_TIMESTAMP.test(value) || CLOCK_TIME.test(value)
  if (REQUEST_ID_KEYS.has(key)) return REQUEST_ID_VALUE.test(value) && !value.includes("://")
  return false
}

/**
 * Move only leading, standalone metadata lines after the stable content.
 * Fenced blocks and ambiguous/prose lines are deliberately left byte-for-byte unchanged.
 */
function moveStablePrefixMetadata(content: string): StablePrefixResult {
  if (!content || !content.includes("\n")) return unchangedPrefixResult(content)

  const lines = splitLinesPreservingEndings(content)
  if (lines.some((line) => isFenceDelimiter(line.text))) return unchangedPrefixResult(content)

  let metadataEnd = 0
  while (metadataEnd < lines.length) {
    const line = lines[metadataEnd]
    if (line === undefined || !isStandaloneMetadataLine(line.text)) break
    metadataEnd++
  }
  if (metadataEnd === 0 || metadataEnd === lines.length) return unchangedPrefixResult(content)

  const bodyLines = lines.slice(metadataEnd)
  const body = bodyLines.map((line) => line.text + line.ending).join("")
  if (!body.trim()) return unchangedPrefixResult(content)

  const movedRecords = lines.slice(0, metadataEnd)
  const finalEnding = content.endsWith("\r\n") ? "\r\n" : content.endsWith("\n") ? "\n" : ""
  const moved = movedRecords
    .map((line, index) => line.text + (index === movedRecords.length - 1 ? finalEnding : line.ending))
    .join("")
  const lastMovedRecord = movedRecords[movedRecords.length - 1]
  const boundary = body.endsWith("\n") ? "" : (lastMovedRecord?.ending ?? "\n")
  const transformed = body + boundary + moved

  return { content: transformed, changed: true, movedLines: movedRecords.map((line) => line.text) }
}

function isMetadataOnlyBlock(block: string): boolean {
  if (!block || block.split(/\r\n|\n/).some(isFenceDelimiter)) return false
  const lines = block.split(/\r\n|\n/).filter((line) => line.trim() !== "")
  return lines.length > 0 && lines.every(isStandaloneMetadataLine)
}

/** Move leading metadata-only system blocks after the stable system blocks. */
function stabilizeSystemBlocks(blocks: readonly string[]): StableSystemResult {
  const transformed = blocks.map((block) => moveStablePrefixMetadata(block).content)
  let leadingMetadataBlocks = 0
  while (leadingMetadataBlocks < blocks.length) {
    const block = blocks[leadingMetadataBlocks]
    if (block === undefined || !isMetadataOnlyBlock(block)) break
    leadingMetadataBlocks++
  }

  const reordered =
    leadingMetadataBlocks > 0 && leadingMetadataBlocks < blocks.length
      ? [...transformed.slice(leadingMetadataBlocks), ...transformed.slice(0, leadingMetadataBlocks)]
      : transformed
  const changed = reordered.some((block, index) => block !== blocks[index])
  const movedBlocks =
    leadingMetadataBlocks > 0 && leadingMetadataBlocks < blocks.length
      ? blocks.slice(0, leadingMetadataBlocks)
      : []

  return { blocks: reordered, changed, movedBlocks }
}

/** A bounded, deterministic fallback fingerprint. Callers must still compare full content. */
function boundedFallbackHash(content: string): string {
  let hash = FNV_OFFSET
  for (const character of content) {
    hash ^= BigInt(character.codePointAt(0) ?? 0)
    hash = BigInt.asUintN(64, hash * FNV_PRIME)
  }
  hash ^= BigInt(content.length)
  return `fallback-${BigInt.asUintN(64, hash).toString(16).padStart(16, "0")}`
}

function bytesToHex(bytes: Uint8Array): string {
  return Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("")
}

function debugEnabled(): boolean {
  return Boolean(process.env.TOKENMILL_DEBUG)
}

/** Prefer cryptographic SHA-256; the bounded fingerprint is only a last-resort scope hint. */
async function hashContent(content: string): Promise<string> {
  const subtle = globalThis.crypto?.subtle
  if (subtle && typeof subtle.digest === "function" && typeof TextEncoder !== "undefined") {
    try {
      const digest = await subtle.digest("SHA-256", new TextEncoder().encode(content))
      return bytesToHex(new Uint8Array(digest))
    } catch (error) {
      if (debugEnabled()) console.debug("[tokenmill] Web Crypto SHA-256 unavailable; trying native SHA-256", error)
    }
  }

  try {
    const { createHash } = await import("node:crypto")
    return createHash("sha256").update(content, "utf8").digest("hex")
  } catch (error) {
    if (debugEnabled()) console.debug("[tokenmill] native SHA-256 unavailable; using bounded fallback", error)
  }

  return boundedFallbackHash(content)
}

/** Serialize JSON-like tool schemas with sorted object keys for deterministic scope keys. */
function stableSerialize(value: unknown): string {
  const ancestors = new WeakSet<object>()

  const serialize = (current: unknown): string => {
    if (current === null) return "null"
    if (typeof current === "string") return JSON.stringify(current)
    if (typeof current === "boolean") return current ? "true" : "false"
    if (typeof current === "number") {
      return Number.isFinite(current) ? String(current) : `number:${String(current)}`
    }
    if (typeof current === "bigint") return `bigint:${current.toString()}`
    if (typeof current === "undefined") return "undefined"
    if (typeof current === "function") return "function"
    if (typeof current === "symbol") return `symbol:${String(current)}`
    if (!isRecord(current)) return String(current)
    if (ancestors.has(current)) return '"[Circular]"'

    ancestors.add(current)
    let result: string
    if (Array.isArray(current)) {
      result = `[${current.map(serialize).join(",")}]`
    } else {
      result = `{${Object.keys(current)
        .sort()
        .map((key) => `${JSON.stringify(key)}:${serialize(current[key])}`)
        .join(",")}}`
    }
    ancestors.delete(current)
    return result
  }

  return serialize(value)
}

function normalizeScopePart(value: unknown): string {
  const text = typeof value === "string" ? value.trim().toLowerCase() : String(value ?? "unknown")
  return text.replace(/[^a-z0-9._-]+/g, "-").replace(/^-+|-+$/g, "") || "unknown"
}

/** Build a provider-neutral scope from a tool name and a canonicalized schema. */
async function createToolSchemaCacheScope(tool: unknown, schema?: unknown): Promise<string> {
  const toolName = normalizeScopePart(tool)
  const schemaHash = await hashContent(stableSerialize(schema))
  return `tokenmill:tool:${toolName}:schema:${schemaHash}`
}

/** Create metadata values without translating them into a provider-specific request. */
function createCacheMetadata(scope: string): Record<string, unknown> {
  return {
    cache_control: { type: "ephemeral" },
    prompt_cache_key: scope,
  }
}

/** Expose cache fields only when explicitly enabled and only through output.metadata. */
function applyCacheMetadata(
  output: unknown,
  metadata: Record<string, unknown>,
  enabled: boolean,
): boolean {
  if (!enabled || !isRecord(output) || !isRecord(metadata)) return false
  if (!("metadata" in output)) return false

  let target = output.metadata
  if (target === undefined) {
    target = {}
    output.metadata = target
  }
  if (!isRecord(target) || Array.isArray(target)) return false

  for (const [key, value] of Object.entries(metadata)) {
    if (!(key in target)) target[key] = value
  }
  return true
}

function isCacheMetadataEnabled(value: unknown = process.env.TOKENMILL_CACHE_METADATA): boolean {
  return typeof value === "string" && /^(?:1|true|yes|on)$/i.test(value.trim())
}

function isTokenMillEnabled(value: unknown = process.env.TOKENMILL_ENABLED): boolean {
  if (typeof value !== "string" || value.trim() === "") return true
  if (/^(?:0|false|no|off)$/i.test(value.trim())) return false
  return true
}

function reportHookFailure(name: string, error: unknown): void {
  console.warn(`[tokenmill] ${name} failed; continuing (fail-open)`, error)
}

const tokenmillPlugin: Plugin = async ({ $ }) => {
  if (!isTokenMillEnabled()) {
    console.debug("[tokenmill] disabled by TOKENMILL_ENABLED")
    return {}
  }

  // Keep the optional binary fail-open and preserve the install diagnostic.
  try {
    await $`which tokenmill`.quiet()
  } catch {
    console.debug("[tokenmill] tokenmill binary not found in PATH — plugin disabled (fail-open)")
    return {}
  }

  // No model-facing ref is emitted: tokenmill_expand is not registered, so replacing content
  // would be lossy for the model. Avoid retaining output content when no dedup path is active.
  const cacheMetadataEnabled = isCacheMetadataEnabled()
  const toolSchemas = new Map<string, unknown>()

  return {
    // The verified definition hook supplies the schema used for the optional cache scope.
    "tool.definition": async (input, output) => {
      try {
        if (cacheMetadataEnabled) toolSchemas.set(normalizeScopePart(input.toolID), output.parameters)
      } catch (error) {
        reportHookFailure("tool.definition", error)
      }
    },
    "tool.execute.after": async (input, output) => {
      try {
        if (!cacheMetadataEnabled) return

        const scope = await createToolSchemaCacheScope(input.tool, toolSchemas.get(normalizeScopePart(input.tool)))
        applyCacheMetadata(output, createCacheMetadata(scope), true)
      } catch (error) {
        reportHookFailure("tool.execute.after", error)
      }
    },
    "experimental.chat.system.transform": async (_input, output) => {
      try {
        const stable = stabilizeSystemBlocks(output.system)
        if (stable.changed) output.system.splice(0, output.system.length, ...stable.blocks)
      } catch (error) {
        reportHookFailure("system.transform", error)
      }
    },
    config: async (cfg) => {
      try {
        // Config.plugin is OpenCode's plugin load list. The plugin is already loaded;
        // do not inject unsupported provider fields or a duplicate self-entry here.
        if (cfg.plugin !== undefined && !Array.isArray(cfg.plugin)) {
          throw new TypeError("OpenCode config.plugin must be an array")
        }
      } catch (error) {
        console.error("[tokenmill] config hook failed (fail-open)", error)
      }
    },
  }
}

// OpenCode's legacy loader treats every runtime named export as a plugin factory.
// Keep test access attached to the sole callable export instead of exporting helpers.
export const TokenMillPlugin = Object.assign(tokenmillPlugin, {
  __testing: {
    isStandaloneMetadataLine,
    moveStablePrefixMetadata,
    stabilizeSystemBlocks,
    boundedFallbackHash,
    hashContent,
    stableSerialize,
    createToolSchemaCacheScope,
    createCacheMetadata,
    applyCacheMetadata,
    isCacheMetadataEnabled,
    isTokenMillEnabled,
  },
})
