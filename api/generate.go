package api

//go:generate sh -c "mkdir -p ../gen/api && go tool oapi-codegen --config oapi-codegen.yaml openapi/platform-v1.yaml"
//go:generate pnpm --dir ../web generate
//go:generate sh -c "mkdir -p ../gen/internalgraph && go tool oapi-codegen --config internal/oapi-codegen.yaml internal/graph-v1.yaml"
