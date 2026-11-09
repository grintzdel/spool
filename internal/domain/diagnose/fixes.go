package diagnose

type Fix struct {
	Framework string
	Snippet   string
}

func Fixes() []Fix {
	return []Fix{
		{"Express", "app.post('/webhook', express.raw({ type: 'application/json' }), handler) and verify req.body (a Buffer)"},
		{"NestJS", "NestFactory.create(AppModule, { rawBody: true }) then verify req.rawBody"},
		{"Fastify", "app.addContentTypeParser('application/json', { parseAs: 'buffer' }, (req, body, done) => done(null, body))"},
		{"Go", "body, _ := io.ReadAll(r.Body) before any json.Unmarshal, and verify body"},
	}
}
