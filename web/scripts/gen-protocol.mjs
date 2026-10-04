// Generates src/protocol.ts from the JSON Schema that cmd/gen-schema writes
// from the Go structs. Run through `make gen`; CI fails if the committed
// output is stale.
import { readFile, writeFile } from 'node:fs/promises'
import { compile } from 'json-schema-to-typescript'

const schemaPath = new URL('../../schema/hm-protocol-v1.json', import.meta.url)
const outPath = new URL('../src/protocol.ts', import.meta.url)

const schema = JSON.parse(await readFile(schemaPath, 'utf8'))

const banner = [
  '/* eslint-disable */',
  '/**',
  ' * GENERATED FILE - do not edit.',
  ' * Source: internal/protocol (Go structs) -> schema/hm-protocol-v1.json -> this file.',
  ' * Regenerate with `make gen`.',
  ' */',
].join('\n')

const ts = await compile(schema, 'HmProtocolV1', {
  bannerComment: banner,
  additionalProperties: false,
  style: { semi: false, singleQuote: true, printWidth: 100 },
})

await writeFile(outPath, ts, 'utf8')
console.log(`wrote ${outPath.pathname}`)
