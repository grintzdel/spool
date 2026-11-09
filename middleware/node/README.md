# @spool/echo

Dev-only middleware that tells [Spool](../../) which request body your app actually saw, so Spool can explain why a
webhook signature check failed. The usual cause is an app that parses and re-serializes the JSON before computing the
HMAC.

No runtime dependencies. Node >= 20.

## Install

```sh
pnpm add -D @spool/echo
```

## Express

```ts
import express from 'express'
import { echo } from '@spool/echo'

const app = express()
app.use(echo())
app.post('/webhooks/stripe', express.raw({ type: 'application/json' }), handler)
```

Register `echo()` early. It only looks at the body once the response has finished, so it sees whatever your body
parsers and handler left on `req`.

## NestJS

Turn on `rawBody` so Spool can compare the raw bytes too:

```ts
const app = await NestFactory.create(AppModule, { rawBody: true })
```

Then apply the middleware:

```ts
import { SpoolEchoMiddleware } from '@spool/echo'

export class AppModule implements NestModule {
  configure(consumer: MiddlewareConsumer) {
    consumer.apply(SpoolEchoMiddleware).forRoutes('*')
  }
}
```

`consumer.apply(echo()).forRoutes('*')` works as well, and lets you pass options.

## What it sends

The middleware only acts when a request forwarded by Spool (it carries an `X-Spool-Id` header) gets a response with
status >= 400. Then it sends:

```
POST http://127.0.0.1:7778/api/requests/{id}/seen
Content-Type: application/octet-stream
X-Spool-Seen-Sha256: <hex sha256 of the body>

<body as the app saw it>
```

It picks the body from the first source that is set:

1. `req.body` as a `Buffer`/`Uint8Array`, e.g. from `express.raw()`
2. `req.rawBody` as a `Buffer`, e.g. from NestJS `rawBody: true`
3. `req.body` as a string, encoded as UTF-8
4. `req.body` as an object, sent as `JSON.stringify(req.body)`, which is what a naive handler passes to the verifier

The request is fire-and-forget. If Spool is not running, is slow or returns an error, nothing happens: your response
is never delayed, changed or broken.

## Options

```ts
echo({
  baseUrl: 'http://127.0.0.1:7778', // Spool API
  enabled: true, // defaults to NODE_ENV !== 'production'
  timeoutMs: 1000,
  bodyOf: (req) => req.verifiedPayload, // Uint8Array | string | object | undefined
})
```

## Dev only

When `NODE_ENV` is `production` the middleware just calls `next()`. Pass `enabled: true` or `enabled: false` to
override that.
