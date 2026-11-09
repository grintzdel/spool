import { createHash } from 'node:crypto'

export type EchoRequest = {
  headers: Record<string, string | string[] | undefined>
  body?: unknown
  rawBody?: unknown
}

export type EchoResponse = {
  statusCode: number
  once(event: 'finish', listener: () => void): unknown
}

export type EchoNext = (error?: unknown) => void

export type BodySource = Uint8Array | string | object | undefined

export interface EchoOptions {
  baseUrl?: string
  enabled?: boolean
  timeoutMs?: number
  bodyOf?: (req: EchoRequest) => BodySource
}

export type EchoMiddleware = (req: EchoRequest, res: EchoResponse, next: EchoNext) => void

const DEFAULT_BASE_URL = 'http://127.0.0.1:7778'
const DEFAULT_TIMEOUT_MS = 1000

const toBytes = (value: BodySource): Buffer | undefined => {
  if (value instanceof Uint8Array) return Buffer.from(value)
  if (typeof value === 'string') return Buffer.from(value, 'utf8')
  if (value !== null && typeof value === 'object') return Buffer.from(JSON.stringify(value), 'utf8')
  return undefined
}

const defaultBodyOf = (req: EchoRequest): BodySource => {
  if (req.body instanceof Uint8Array) return req.body
  if (req.rawBody instanceof Uint8Array) return req.rawBody
  if (typeof req.body === 'string') return req.body
  if (req.body !== null && typeof req.body === 'object') return req.body
  return undefined
}

const spoolIdOf = (req: EchoRequest): string | undefined => {
  const header = req.headers['x-spool-id']
  const id = Array.isArray(header) ? header[0] : header
  return id ? id : undefined
}

const report = async (url: string, bytes: Buffer, timeoutMs: number) => {
  try {
    await fetch(url, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/octet-stream',
        'X-Spool-Seen-Sha256': createHash('sha256').update(bytes).digest('hex'),
      },
      body: bytes,
      signal: AbortSignal.timeout(timeoutMs),
    })
  } catch {}
}

export function echo(options: EchoOptions = {}): EchoMiddleware {
  const enabled = options.enabled ?? process.env.NODE_ENV !== 'production'
  if (!enabled) return (_req, _res, next) => next()

  const baseUrl = (options.baseUrl ?? DEFAULT_BASE_URL).replace(/\/+$/, '')
  const timeoutMs = options.timeoutMs ?? DEFAULT_TIMEOUT_MS
  const bodyOf = options.bodyOf ?? defaultBodyOf

  return (req, res, next) => {
    const id = spoolIdOf(req)
    if (id) {
      res.once('finish', () => {
        if (res.statusCode < 400) return
        let bytes: Buffer | undefined
        try {
          bytes = toBytes(bodyOf(req))
        } catch {
          return
        }
        if (bytes) void report(`${baseUrl}/api/requests/${encodeURIComponent(id)}/seen`, bytes, timeoutMs)
      })
    }
    next()
  }
}

export class SpoolEchoMiddleware {
  private readonly middleware: EchoMiddleware

  constructor(options?: EchoOptions) {
    this.middleware = echo(options)
  }

  use(req: EchoRequest, res: EchoResponse, next: EchoNext): void {
    this.middleware(req, res, next)
  }
}
