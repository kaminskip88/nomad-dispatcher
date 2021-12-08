BINARY=nomad-dispatcher

build:
	@go build -o ${BINARY}

testacc: up
	@./${BINARY} -addr=http://localhost:4646 \
		-m "META_DATA_1=meta1" -m "META_DATA_2=meta2" \
		-payload=docker-compose.yaml test

up:
	@docker-compose up -d
	@sleep 3
	@while curl -s http://localhost:4646/v1/job/test | grep -q "job not found"; do\
    sleep 1 ; \
    echo "waiting for job to create"; \
	done;

down:
	@docker-compose down

clean: down
	@rm -f ${BINARY}

logs:
	@docker-compose logs rundeck
