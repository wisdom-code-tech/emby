.PHONY: fetch gateway validate tools build clean

fetch:
	./scripts/fetch-upstream.sh

gateway:
	./scripts/build-gateway.sh

validate:
	./scripts/validate.sh

tools:
	./scripts/bootstrap-fnpack.sh

build: tools gateway fetch validate
	./scripts/build.sh

clean:
	rm -rf dist packaging/emby/app/server/emby-server \
		packaging/emby/app/server/gateway-proxy packaging/emby/*.fpk
