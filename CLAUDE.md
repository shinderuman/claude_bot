# claude_bot プロジェクトルール

## test_claude による動作確認

- 実行時はシェルの Claude Code 用環境変数を外すこと:

```bash
env -u ANTHROPIC_BASE_URL -u ANTHROPIC_AUTH_TOKEN ./test_claude -message "こんにちは" data/.env.<プロバイダー名>
```

- 理由: `config.LoadEnvironment` 内の `godotenv.Load` は既存の環境変数を上書きしないため、シェルに `ANTHROPIC_BASE_URL` / `ANTHROPIC_AUTH_TOKEN` が設定されていると `.env.<プロバイダー名>` 側の `ANTHROPIC_BASE_URL`（OpenRouter等）とトークンが無視され、意図しないAPIエンドポイントへリクエストが飛ぶ（Unknown Model 400 の原因）
- フラグ（`-mode`, `-message` 等）はenvパスより前に置くこと。Goのflagパッケージは最初の位置引数以降のフラグを解析しない
