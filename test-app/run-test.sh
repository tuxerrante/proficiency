#!/bin/bash
# filepath: test-app/run-test.sh

set -e

echo "==> Step 1: Generate OpenAPI 3.1 spec"
swag init --output ./docs --outputTypes yaml --v3.1
echo "✓ Generated: docs/swagger.yaml (OpenAPI 3.1)"

# Fix externalDocs issue (swag generates empty url which fails validation)
echo "==> Fixing OpenAPI spec..."
sed -i.bak '/^externalDocs:/,/^  url: ""$/d' docs/swagger.yaml && rm docs/swagger.yaml.bak
echo "✓ Removed empty externalDocs"

echo ""
echo "==> Step 2: Build test service"
go build -o test-service main.go

echo ""
echo "==> Step 3: Start test service"
./test-service &
SERVICE_PID=$!

sleep 3

echo ""
echo "==> Step 4: Verify service is running"
curl -s http://localhost:6060/health | jq .
echo "✓ Swagger UI: http://localhost:6060/swagger/index.html"

echo ""
echo "==> Step 5: Run proficiency with generated YAML"
cd ..
go build -o proficiency ./cmd/proficiency

./proficiency \
  --swagger ./test-app//docs/swagger.yaml \
  --target http://localhost:6060 \
  --duration 30s \
  --concurrency 5 \
  --rps 50

echo ""
echo "==> Step 6: Cleanup"
kill $SERVICE_PID

echo ""
echo "==> ✅ Test complete!"
echo "Generated OpenAPI: api-generated.yaml"
echo "Profiles: ./profiles/"
ls -lh ./profiles/