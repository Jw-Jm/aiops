package api

//go:generate sh -c "mkdir -p ../gen/api && go tool oapi-codegen --config oapi-codegen.yaml openapi/platform-v1.yaml"
//go:generate pnpm --dir ../web generate
