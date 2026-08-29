import assert from "node:assert/strict"
import test from "node:test"
import * as tokenmill from "./tokenmill"

type Helpers = {
  hashContent: (content: string) => Promise<string>
  boundedFallbackHash: (content: string) => string
  moveStablePrefixMetadata: (content: string) => {
    content: string
    changed: boolean
    movedLines: string[]
  }
  stabilizeSystemBlocks: (blocks: readonly string[]) => {
    blocks: string[]
    changed: boolean
    movedBlocks: string[]
  }
  createToolSchemaCacheScope: (tool: unknown, schema: unknown) => Promise<string>
  createCacheMetadata: (scope: string) => Record<string, unknown>
  applyCacheMetadata: (
    output: unknown,
    metadata: Record<string, unknown>,
    enabled: boolean,
  ) => boolean
}

type Hooks = {
  "tool.execute.before"?: (input: unknown, output: unknown) => Promise<void>
  "tool.execute.after"?: (input: unknown, output: unknown) => Promise<void>
  "tool.definition"?: (input: unknown, output: unknown) => Promise<void>
  "experimental.chat.messages.transform"?: (input: unknown, output: unknown) => Promise<void>
  "experimental.chat.system.transform"?: (input: unknown, output: unknown) => Promise<void>
  config?: (config: unknown) => Promise<void>
}

const helpers = tokenmill.TokenMillPlugin.__testing as Helpers

test("exports only the loader-safe plugin factory", () => {
  assert.deepEqual(Object.keys(tokenmill).sort(), ["TokenMillPlugin"])
  assert.equal(typeof tokenmill.TokenMillPlugin, "function")
})

function shellPromise(stdout: string): Promise<unknown> & {
  quiet: () => ReturnType<typeof shellPromise>
  nothrow: () => ReturnType<typeof shellPromise>
} {
  const promise = Promise.resolve({ stdout: Buffer.from(stdout), stderr: Buffer.alloc(0), exitCode: 0 }) as Promise<unknown> & {
    quiet: () => ReturnType<typeof shellPromise>
    nothrow: () => ReturnType<typeof shellPromise>
  }
  promise.quiet = () => promise
  promise.nothrow = () => promise
  return promise
}

function createTestShell() {
  return ((strings: TemplateStringsArray, ...values: unknown[]) => {
    const command = strings.reduce(
      (result, string, index) => result + string + (values[index] === undefined ? "" : String(values[index])),
      "",
    )
    if (command === "which tokenmill") return shellPromise("")
    return shellPromise("")
  }) as never
}

async function createHooks(): Promise<Hooks> {
  const plugin = await tokenmill.TokenMillPlugin({ $: createTestShell() } as never)
  return plugin as Hooks
}

test("hashContent returns the native SHA-256 digest", async () => {
  assert.equal(typeof helpers.hashContent, "function")
  const hashContent = helpers.hashContent
  assert.ok(hashContent)

  assert.equal(
    await hashContent("hello"),
    "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824",
  )
})

test("hashContent keeps SHA-256 when Web Crypto is unavailable", async () => {
  assert.equal(typeof helpers.hashContent, "function")
  const hashContent = helpers.hashContent
  assert.ok(hashContent)

  await import("node:crypto")
  const previousCrypto = globalThis.crypto
  try {
    Object.defineProperty(globalThis, "crypto", { value: undefined, configurable: true })
    assert.equal(
      await hashContent("hello"),
      "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824",
    )
  } finally {
    Object.defineProperty(globalThis, "crypto", { value: previousCrypto, configurable: true })
  }
})

test("boundedFallbackHash is deterministic and has a bounded width", () => {
  assert.equal(typeof helpers.boundedFallbackHash, "function")
  const boundedFallbackHash = helpers.boundedFallbackHash
  assert.ok(boundedFallbackHash)

  const first = boundedFallbackHash("same content")
  assert.equal(first, boundedFallbackHash("same content"))
  assert.match(first, /^fallback-[0-9a-f]{16}$/)
  assert.notEqual(first, boundedFallbackHash("different content"))
})

test("moveStablePrefixMetadata moves only standalone metadata lines to a deterministic suffix", () => {
  assert.equal(typeof helpers.moveStablePrefixMetadata, "function")
  const moveStablePrefixMetadata = helpers.moveStablePrefixMetadata
  assert.ok(moveStablePrefixMetadata)

  const timestamp = "timestamp: 2026-08-27T10:11:12Z"
  const requestID = "request-id: 1234-abcd"
  const result = moveStablePrefixMetadata(`${timestamp}\n${requestID}\nactual payload`)

  assert.equal(result.changed, true)
  assert.deepEqual(result.movedLines, [timestamp, requestID])
  assert.equal(result.content, `actual payload\n${timestamp}\n${requestID}`)
})

test("moveStablePrefixMetadata preserves mixed line endings inside the payload", () => {
  assert.equal(typeof helpers.moveStablePrefixMetadata, "function")
  const moveStablePrefixMetadata = helpers.moveStablePrefixMetadata
  assert.ok(moveStablePrefixMetadata)

  const timestamp = "timestamp: 2026-08-27T10:11:12Z"
  const requestID = "request-id: 1234-abcd"
  const result = moveStablePrefixMetadata(`${timestamp}\r\n${requestID}\npayload\nnext line`)

  assert.equal(result.content, `payload\nnext line\n${timestamp}\r\n${requestID}`)
})

test("moveStablePrefixMetadata leaves URLs, prose, and fenced content exact", () => {
  assert.equal(typeof helpers.moveStablePrefixMetadata, "function")
  const moveStablePrefixMetadata = helpers.moveStablePrefixMetadata
  assert.ok(moveStablePrefixMetadata)

  const unchanged = [
    "https://example.test/2026-08-27/request-id",
    "The request-id is 1234-abcd and the date is 2026-08-27.",
    "```text\n2026-08-27\n```\npayload",
  ]

  for (const content of unchanged) {
    assert.deepEqual(moveStablePrefixMetadata(content), {
      content,
      changed: false,
      movedLines: [],
    })
  }
})

test("stabilizeSystemBlocks moves leading metadata blocks after stable blocks", () => {
  assert.equal(typeof helpers.stabilizeSystemBlocks, "function")
  const stabilizeSystemBlocks = helpers.stabilizeSystemBlocks
  assert.ok(stabilizeSystemBlocks)

  const metadata = ["date: 2026-08-27", "uuid: 550e8400-e29b-41d4-a716-446655440000"]
  const result = stabilizeSystemBlocks([...metadata, "stable instructions"])

  assert.equal(result.changed, true)
  assert.deepEqual(result.blocks, ["stable instructions", ...metadata])
  assert.deepEqual(result.movedBlocks, metadata)
})

test("createToolSchemaCacheScope is stable across schema object key order", async () => {
  assert.equal(typeof helpers.createToolSchemaCacheScope, "function")
  const createToolSchemaCacheScope = helpers.createToolSchemaCacheScope
  assert.ok(createToolSchemaCacheScope)

  const first = await createToolSchemaCacheScope("bash", {
    properties: { command: { type: "string" } },
    type: "object",
  })
  const second = await createToolSchemaCacheScope("bash", {
    type: "object",
    properties: { command: { type: "string" } },
  })
  const otherTool = await createToolSchemaCacheScope("read", {
    type: "object",
    properties: { command: { type: "string" } },
  })

  assert.equal(first, second)
  assert.notEqual(first, otherTool)
  assert.match(first, /^tokenmill:tool:bash:schema:(?:[0-9a-f]{64}|fallback-[0-9a-f]{16})$/)
})

test("applyCacheMetadata is opt-in and mutates only an existing metadata field", () => {
  assert.equal(typeof helpers.createCacheMetadata, "function")
  assert.equal(typeof helpers.applyCacheMetadata, "function")
  const createCacheMetadata = helpers.createCacheMetadata
  const applyCacheMetadata = helpers.applyCacheMetadata
  assert.ok(createCacheMetadata)
  assert.ok(applyCacheMetadata)

  const metadata: Record<string, unknown> = { existing: "preserved" }
  const output = { metadata }
  const fields = createCacheMetadata("tokenmill:tool:bash:schema:abc")

  assert.equal(applyCacheMetadata(output, fields, false), false)
  assert.deepEqual(metadata, { existing: "preserved" })

  assert.equal(applyCacheMetadata(output, fields, true), true)
  assert.equal(metadata.existing, "preserved")
  const adaptedMetadata = metadata as Record<string, unknown>
  assert.deepEqual(adaptedMetadata["cache_control"], { type: "ephemeral" })
  assert.equal(adaptedMetadata["prompt_cache_key"], "tokenmill:tool:bash:schema:abc")

  const missingMetadata = {}
  assert.equal(applyCacheMetadata(missingMetadata, fields, true), false)
  assert.deepEqual(missingMetadata, {})
})

test("does not register a message transform that can reorder user text", async () => {
  const hooks = await createHooks()
  assert.equal(hooks["experimental.chat.messages.transform"], undefined)
})

test("system transform preserves the live system array while stabilizing metadata blocks", async () => {
  const hooks = await createHooks()
  assert.ok(hooks["experimental.chat.system.transform"])

  const system = ["date: 2026-08-27", "stable instructions"]
  const originalArray = system
  await hooks["experimental.chat.system.transform"]({}, { system } as never)

  assert.strictEqual(system, originalArray)
  assert.deepEqual(system, ["stable instructions", "date: 2026-08-27"])
})

test("hook failures are reported while remaining fail-open", async () => {
  const hooks = await createHooks()
  assert.ok(hooks["experimental.chat.system.transform"])

  const warnings: unknown[][] = []
  const originalWarn = console.warn
  console.warn = (...arguments_: unknown[]) => warnings.push(arguments_)
  try {
    await hooks["experimental.chat.system.transform"]({}, { system: undefined } as never)
  } finally {
    console.warn = originalWarn
  }

  assert.equal(warnings.length, 1)
  assert.match(String(warnings[0]?.[0]), /system\.transform/)
})

test("does not register a pre-execution shell rewrite hook", async () => {
  const hooks = await createHooks()
  assert.equal(hooks["tool.execute.before"], undefined)
})

test("TOKENMILL_ENABLED=false disables the plugin before registering hooks", async () => {
  const previous = process.env.TOKENMILL_ENABLED
  try {
    process.env.TOKENMILL_ENABLED = "false"
    const hooks = await createHooks()
    assert.deepEqual(hooks, {})
  } finally {
    if (previous === undefined) delete process.env.TOKENMILL_ENABLED
    else process.env.TOKENMILL_ENABLED = previous
  }
})

test("cache metadata is absent by default and exposed only in tool output metadata when opted in", async () => {
  const previous = process.env.TOKENMILL_CACHE_METADATA
  try {
    delete process.env.TOKENMILL_CACHE_METADATA
    const disabledHooks = await createHooks()
    const disabledOutput = { title: "bash", output: "result", metadata: { existing: true } }
    await disabledHooks["tool.execute.after"]?.({ tool: "bash" }, disabledOutput)
    assert.deepEqual(disabledOutput.metadata, { existing: true })

    process.env.TOKENMILL_CACHE_METADATA = "true"
    const enabledHooks = await createHooks()
    const enabledOutput = { title: "bash", output: "result", metadata: { existing: true } }
    await enabledHooks["tool.execute.after"]?.({ tool: "bash" }, enabledOutput)
    assert.equal(enabledOutput.metadata.existing, true)
    const enabledMetadata = enabledOutput.metadata as Record<string, unknown>
    assert.deepEqual(enabledMetadata["cache_control"], { type: "ephemeral" })
    assert.match(String(enabledMetadata["prompt_cache_key"]), /^tokenmill:tool:bash:schema:/)
  } finally {
    if (previous === undefined) delete process.env.TOKENMILL_CACHE_METADATA
    else process.env.TOKENMILL_CACHE_METADATA = previous
  }
})

test("opt-in cache metadata scopes by the canonical tool schema", async () => {
  const previousFlag = process.env.TOKENMILL_CACHE_METADATA
  try {
    process.env.TOKENMILL_CACHE_METADATA = "true"
    const hooks = await createHooks()
    assert.ok(hooks["tool.definition"])
    assert.ok(hooks["tool.execute.after"])

    const schema = { properties: { command: { type: "string" } }, type: "object" }
    await hooks["tool.definition"]({ toolID: "bash" }, { description: "", parameters: schema })
    const output = { title: "bash", output: "result", metadata: {} as Record<string, unknown> }
    await hooks["tool.execute.after"]({ tool: "bash" }, output)

    const expectedScope = await helpers.createToolSchemaCacheScope?.("bash", schema)
    assert.equal(output.metadata.prompt_cache_key, expectedScope)
  } finally {
    if (previousFlag === undefined) delete process.env.TOKENMILL_CACHE_METADATA
    else process.env.TOKENMILL_CACHE_METADATA = previousFlag
  }
})

test("disabled cache metadata does not hash tool output unnecessarily", async () => {
  const previousFlag = process.env.TOKENMILL_CACHE_METADATA
  const previousCrypto = globalThis.crypto
  let digestCalls = 0
  const digest = previousCrypto.subtle.digest.bind(previousCrypto.subtle)
  const countedCrypto = {
    subtle: {
      digest: async (algorithm: AlgorithmIdentifier, data: BufferSource) => {
        digestCalls++
        return digest(algorithm, data)
      },
    },
  } as unknown as Crypto

  try {
    delete process.env.TOKENMILL_CACHE_METADATA
    Object.defineProperty(globalThis, "crypto", { value: countedCrypto, configurable: true })
    const hooks = await createHooks()
    await hooks["tool.execute.after"]?.({ tool: "bash" }, { title: "bash", output: "long output ".repeat(10), metadata: {} })
    assert.equal(digestCalls, 0)
  } finally {
    Object.defineProperty(globalThis, "crypto", { value: previousCrypto, configurable: true })
    if (previousFlag === undefined) delete process.env.TOKENMILL_CACHE_METADATA
    else process.env.TOKENMILL_CACHE_METADATA = previousFlag
  }
})

test("config hook reports malformed config instead of swallowing the exception", async () => {
  const hooks = await createHooks()
  assert.ok(hooks.config)

  const errors: unknown[][] = []
  const originalError = console.error
  console.error = (...arguments_: unknown[]) => errors.push(arguments_)
  try {
    await hooks.config({ plugin: "not-an-array" })
  } finally {
    console.error = originalError
  }

  assert.equal(errors.length, 1)
  assert.match(String(errors[0]?.[0]), /config hook failed/)
})
