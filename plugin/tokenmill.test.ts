import assert from "node:assert/strict"
import test from "node:test"
import * as tokenmill from "./tokenmill"

type Helpers = {
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

test("does not register a message transform that can reorder user text", async () => {
  const hooks = await createHooks()
  assert.equal(hooks["experimental.chat.messages.transform"], undefined)
})

test("does not register tool hooks: cache metadata is fully removed", async () => {
  const hooks = await createHooks()
  assert.equal(hooks["tool.definition"], undefined)
  assert.equal(hooks["tool.execute.after"], undefined)
  assert.equal(hooks["tool.execute.before"], undefined)
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
