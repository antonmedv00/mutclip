build: build-server build-client

push: push-server push-client

build-local: build-server-local build-client-local

init: init-client proto

clean: clean-server clean-client clean-proto


.PHONY: proto
proto:
	mkdir -p client/src/pb
	protoc -I=proto --go_out=server/pkg --ts_proto_out=client/src/pb --plugin=client/node_modules/.bin/protoc-gen-ts_proto proto/*.proto

clean-proto:
	rm -rf server/pkg/pb client/src/pb

reproto: clean-proto proto


build-server:
	docker build server -t aantonm/mutclip:server

push-server:
	docker push aantonm/mutclip:server

build-server-local:
	mkdir -p server/out
	cp .env server/out
	cd server && go build -o out/server -ldflags '-s -w' ./cmd/server && tar czvf out/server.tar.gz --transform 's,^out/,server/,' out/server out/.env

dev-server:
	set -a && . ./.env && set +a && cd server && CI=1 CLICOLOR_FORCE=1 air

clean-server:
	rm -rf server/out


build-client:
	docker build client -t aantonm/mutclip:client

push-client:
	docker push aantonm/mutclip:client

build-client-local:
	mkdir -p client/out
	cp .env client/out
	cd client && npm run build && cp -rT .next/standalone out && cp -r .next/static out/.next && tar czvf out/client.tar.gz --transform 's,^out/,client/,' out/server.js out/package.json out/node_modules out/.next out/.env

dev-client:
	set -a && . ./.env && set +a && cd client && npm run dev

init-client:
	cd client && npm install

clean-client:
	rm -rf client/out client/node_modules client/.next
