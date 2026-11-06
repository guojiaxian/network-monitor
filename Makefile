.PHONY: build agent server web test dist clean

build: agent server web

agent:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o bin/netwatch-agent ./cmd/agent

server:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o bin/netwatch-server ./cmd/server

web:
	cd web && npm install --registry=https://registry.npmmirror.com --no-audit --no-fund && npm run build

test:
	go test ./...

# 交付打包：产出 dist/netwatch-<版本>.tar.gz（含二进制/前端/部署物/文档）与 sha256
DIST_VERSION ?= 0.2
VM_VERSION ?= v1.102.1

# 下载 VictoriaMetrics 单二进制进交付包（多源尝试：GitHub 直连 + 国内镜像加速；已下载则复用）
vm-binary:
	@mkdir -p dist/vm-cache
	@if [ -f dist/vm-cache/victoria-metrics ]; then echo "VM 二进制已存在"; exit 0; fi
	@if [ ! -f dist/vm-cache/vm.tar.gz ]; then ok=0; for base in "" "https://ghproxy.cn/" "https://gh-proxy.com/" "https://mirror.ghproxy.com/"; do \
	  url="$$basehttps://github.com/VictoriaMetrics/VictoriaMetrics/releases/download/$(VM_VERSION)/victoria-metrics-linux-amd64-$(VM_VERSION).tar.gz"; \
	  echo "尝试: $$url"; \
	  if curl -fsSL --max-time 120 -o dist/vm-cache/vm.tar.gz "$$url"; then ok=1; break; fi; \
	  rm -f dist/vm-cache/vm.tar.gz; \
	done; \
	[ $$ok -eq 1 ] || { echo "全部下载源失败：请手动下载 victoria-metrics-linux-amd64-$(VM_VERSION).tar.gz 放入 dist/vm-cache/ 后重试"; exit 1; }; fi
	@tar xzf dist/vm-cache/vm.tar.gz -C dist/vm-cache && mv dist/vm-cache/victoria-metrics-prod dist/vm-cache/victoria-metrics && chmod +x dist/vm-cache/victoria-metrics

dist: build agent vm-binary
	mkdir -p dist/netwatch-$(DIST_VERSION)
	cp -r bin web/dist deploy docs README.md AGENTS.md dist/netwatch-$(DIST_VERSION)/
	cp dist/vm-cache/victoria-metrics dist/netwatch-$(DIST_VERSION)/bin/
	rm -rf dist/netwatch-$(DIST_VERSION)/web/node_modules
	tar czf dist/netwatch-$(DIST_VERSION).tar.gz -C dist netwatch-$(DIST_VERSION)
	@cd dist && sha256sum netwatch-$(DIST_VERSION).tar.gz > netwatch-$(DIST_VERSION).tar.gz.sha256
	@echo "交付包: dist/netwatch-$(DIST_VERSION).tar.gz"

clean:
	rm -rf bin web/dist dist
