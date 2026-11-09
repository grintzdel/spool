import { createHash } from 'node:crypto'
import { createServer, type IncomingMessage, type Server, type ServerResponse } from 'node:http'
import type { AddressInfo } from 'node:net'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { echo, SpoolEchoMiddleware, type EchoOptions } from './index.js'

type Received = { method: string; url: string; contentType: string; sha256: string; body: Buffer }

type SpoolStub = { baseUrl: string; received: Received[]; next: () => Promise<Received>; close: () => Promise<void> }

type AppRequest = IncomingMessage & { body?: unknown; rawBody?: unknown }

type Handler = (req: AppRequest, res: ServerResponse, next: () => void) => void

const SPOOL_ID = '01JC0000000000000000000000'
const QUIET_MS = 150

const listen = (server: Server) =>
  new Promise<string>((resolve) => {
    server.listen(0, '127.0.0.1', () => resolve(`http://127.0.0.1:${(server.address() as AddressInfo).port}`))
  })

const closeServer = (server: Server) => new Promise<void>((resolve) => server.close(() => resolve()))

const startSpool = async (): Promise<SpoolStub> => {
  const received: Received[] = []
  const waiters: Array<(r: Received) => void> = []
  const server = createServer((req, res) => {
    const chunks: Buffer[] = []
    req.on('data', (c: Buffer) => chunks.push(c))
    req.on('end', () => {
      const entry: Received = {
        method: req.method ?? '',
        url: req.url ?? '',
        contentType: String(req.headers['content-type']),
        sha256: String(req.headers['x-spool-seen-sha256']),
        body: Buffer.concat(chunks),
      }
      received.push(entry)
      res.statusCode = 204
      res.end()
      waiters.shift()?.(entry)
    })
  })
  const baseUrl = await listen(server)
  return {
    baseUrl,
    received,
    next: () => new Promise((resolve) => waiters.push(resolve)),
    close: () => closeServer(server),
  }
}

const startApp = async (middleware: Handler, prepare: (req: AppRequest) => void, status: number) => {
  const server = createServer((req: AppRequest, res) => {
    prepare(req)
    middleware(req, res, () => {
      res.statusCode = status
      res.end('handled')
    })
  })
  const baseUrl = await listen(server)
  return { baseUrl, close: () => closeServer(server) }
}

const hit = async (
  middleware: Handler,
  {
    prepare = () => {},
    status = 401,
    withId = true,
  }: { prepare?: (req: AppRequest) => void; status?: number; withId?: boolean }
) => {
  const app = await startApp(middleware, prepare, status)
  try {
    const res = await fetch(`${app.baseUrl}/webhook`, {
      method: 'POST',
      headers: withId ? { 'X-Spool-Id': SPOOL_ID } : {},
      body: '{"a": 1}',
    })
    return { status: res.status, text: await res.text() }
  } finally {
    await app.close()
  }
}

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms))

const sha256 = (bytes: Buffer) => createHash('sha256').update(bytes).digest('hex')

describe('echo', () => {
  let spool: SpoolStub
  let options: EchoOptions

  beforeEach(async () => {
    spool = await startSpool()
    options = { baseUrl: spool.baseUrl, enabled: true }
  })

  afterEach(async () => {
    await spool.close()
  })

  it('sends the seen body to Spool when the app answers >= 400 for a Spool request', async () => {
    const seen = spool.next()
    await hit(echo(options), { prepare: (req) => (req.body = Buffer.from('raw-bytes')) })

    const received = await seen
    expect(received.method).toBe('POST')
    expect(received.url).toBe(`/api/requests/${SPOOL_ID}/seen`)
    expect(received.contentType).toBe('application/octet-stream')
    expect(received.body.toString()).toBe('raw-bytes')
  })

  it('sets X-Spool-Seen-Sha256 to the lowercase hex sha256 of the sent bytes', async () => {
    const seen = spool.next()
    await hit(echo(options), { prepare: (req) => (req.body = Buffer.from('raw-bytes')) })

    const received = await seen
    expect(received.sha256).toBe(sha256(Buffer.from('raw-bytes')))
    expect(received.sha256).toMatch(/^[0-9a-f]{64}$/)
  })

  it('does not send when the app answers 2xx', async () => {
    await hit(echo(options), { prepare: (req) => (req.body = Buffer.from('ok')), status: 200 })
    await sleep(QUIET_MS)

    expect(spool.received).toHaveLength(0)
  })

  it('does not send when the request has no X-Spool-Id', async () => {
    await hit(echo(options), { prepare: (req) => (req.body = Buffer.from('x')), withId: false })
    await sleep(QUIET_MS)

    expect(spool.received).toHaveLength(0)
  })

  it('reads the body at finish time, after the handler ran', async () => {
    const seen = spool.next()
    const middleware = echo(options)
    await hit(
      (req, res, next) =>
        middleware(req, res, () => {
          req.body = Buffer.from('set-by-handler')
          next()
        }),
      {}
    )

    expect((await seen).body.toString()).toBe('set-by-handler')
  })

  describe('body source priority', () => {
    it('prefers a Uint8Array req.body over req.rawBody', async () => {
      const seen = spool.next()
      await hit(echo(options), {
        prepare: (req) => {
          req.body = new Uint8Array([104, 105])
          req.rawBody = Buffer.from('raw')
        },
      })

      expect((await seen).body.toString()).toBe('hi')
    })

    it('uses req.rawBody when req.body is a parsed object', async () => {
      const seen = spool.next()
      await hit(echo(options), {
        prepare: (req) => {
          req.body = { a: 1 }
          req.rawBody = Buffer.from('{"a": 1}')
        },
      })

      expect((await seen).body.toString()).toBe('{"a": 1}')
    })

    it('uses req.rawBody over a string req.body', async () => {
      const seen = spool.next()
      await hit(echo(options), {
        prepare: (req) => {
          req.body = 'text'
          req.rawBody = Buffer.from('raw')
        },
      })

      expect((await seen).body.toString()).toBe('raw')
    })

    it('encodes a string req.body as utf-8', async () => {
      const seen = spool.next()
      await hit(echo(options), { prepare: (req) => (req.body = 'héllo') })

      expect((await seen).body).toEqual(Buffer.from('héllo', 'utf8'))
    })

    it('re-serializes an object req.body with JSON.stringify', async () => {
      const seen = spool.next()
      await hit(echo(options), { prepare: (req) => (req.body = { a: 1, b: [true] }) })

      expect((await seen).body.toString()).toBe('{"a":1,"b":[true]}')
    })

    it('sends nothing when no body source is available', async () => {
      await hit(echo(options), {})
      await sleep(QUIET_MS)

      expect(spool.received).toHaveLength(0)
    })
  })

  it('uses a custom bodyOf over the default sources', async () => {
    const seen = spool.next()
    await hit(echo({ ...options, bodyOf: () => 'custom' }), { prepare: (req) => (req.body = Buffer.from('ignored')) })

    expect((await seen).body.toString()).toBe('custom')
  })

  it('normalizes an object returned by bodyOf with JSON.stringify', async () => {
    const seen = spool.next()
    await hit(echo({ ...options, bodyOf: () => ({ z: 0 }) }), {})

    expect((await seen).body.toString()).toBe('{"z":0}')
  })

  it('sends nothing when bodyOf returns undefined', async () => {
    await hit(echo({ ...options, bodyOf: () => undefined }), { prepare: (req) => (req.body = Buffer.from('x')) })
    await sleep(QUIET_MS)

    expect(spool.received).toHaveLength(0)
  })

  describe('enablement', () => {
    const originalEnv = process.env.NODE_ENV

    afterEach(() => {
      process.env.NODE_ENV = originalEnv
    })

    it('is a pass-through when NODE_ENV is production', async () => {
      process.env.NODE_ENV = 'production'
      const result = await hit(echo({ baseUrl: spool.baseUrl }), { prepare: (req) => (req.body = Buffer.from('x')) })
      await sleep(QUIET_MS)

      expect(result).toEqual({ status: 401, text: 'handled' })
      expect(spool.received).toHaveLength(0)
    })

    it('can be forced on in production with enabled: true', async () => {
      process.env.NODE_ENV = 'production'
      const seen = spool.next()
      await hit(echo({ baseUrl: spool.baseUrl, enabled: true }), { prepare: (req) => (req.body = Buffer.from('x')) })

      expect((await seen).body.toString()).toBe('x')
    })

    it('can be forced off with enabled: false', async () => {
      await hit(echo({ baseUrl: spool.baseUrl, enabled: false }), { prepare: (req) => (req.body = Buffer.from('x')) })
      await sleep(QUIET_MS)

      expect(spool.received).toHaveLength(0)
    })
  })

  it('swallows errors and leaves the response intact when Spool is down', async () => {
    await spool.close()
    const unhandled: unknown[] = []
    const onUnhandled = (reason: unknown) => unhandled.push(reason)
    process.on('unhandledRejection', onUnhandled)
    try {
      const result = await hit(echo(options), { prepare: (req) => (req.body = Buffer.from('x')) })
      await sleep(QUIET_MS)

      expect(result).toEqual({ status: 401, text: 'handled' })
      expect(unhandled).toEqual([])
    } finally {
      process.off('unhandledRejection', onUnhandled)
      spool = await startSpool()
    }
  })

  it('does not wait for a slow Spool and gives up after timeoutMs', async () => {
    const hanging = createServer(() => {})
    const hangingUrl = await listen(hanging)
    try {
      const started = Date.now()
      const result = await hit(echo({ baseUrl: hangingUrl, enabled: true, timeoutMs: 50 }), {
        prepare: (req) => (req.body = Buffer.from('x')),
      })

      expect(result.status).toBe(401)
      expect(Date.now() - started).toBeLessThan(500)
    } finally {
      hanging.closeAllConnections()
      await closeServer(hanging)
    }
  })
})

describe('SpoolEchoMiddleware', () => {
  it('sends the seen body like echo() does', async () => {
    const spool = await startSpool()
    try {
      const instance = new SpoolEchoMiddleware({ baseUrl: spool.baseUrl })
      const seen = spool.next()
      const result = await hit((req, res, next) => instance.use(req, res, next), {
        prepare: (req) => (req.body = Buffer.from('nest')),
      })

      expect(result).toEqual({ status: 401, text: 'handled' })
      expect((await seen).body.toString()).toBe('nest')
    } finally {
      await spool.close()
    }
  })
})
