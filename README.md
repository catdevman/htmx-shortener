===== README.md =====

# Prereqs

- Docker
- Go
- awslocal
- Air (optional)

# Setup

- Get DDB running locally with localstack
```sh { name=localstack background=true }
$ docker compose up -d
```

- Create test DDB table

```sh { name=createtable }
$ aws --endpoint-url=http://127.0.0.1:8000 dynamodb create-table --table-name test --attribute-definitions AttributeName=pk,AttributeType=S AttributeName=sk,AttributeType=S --key-schema AttributeName=pk,KeyType=HASH AttributeName=sk,KeyType=RANGE --billing-mode PAY_PER_REQUEST
```

# Run App

- optional, this should hot rebuild for view and code changes but it wasn't always working perfectly
```sh { name=air background=true }
$ export OAUTH_KEY=set_key
$ export OAUTH_SECRET=set_secret
$ export OAUTH_DOMAIN=set_domain
$ export DDB_TABLE=test
$ cd shortener
$ air
```
- Use the go cli to run the project 
```sh { name=go }
$ export OAUTH_KEY=set_key
$ export OAUTH_SECRET=set_secret
$ export OAUTH_DOMAIN=set_domain
$ export DDB_TABLE=test
$ cd shortener
$ go run ./
```

```sh {name=ngrok }
$ ngrok start --all
```
# Using

- Go to http://127.0.0.1:8989/ (https://big-beloved-pup.ngrok-free.app/) to login
- After login with Google it should redirect you to the home screen

# TODOs

- [x] Help Screen to show keyboard shortcuts
- [x] Setup keyboard shortcuts for adding url and showing help screen
- [ ] Refactor DDB usage and fiber setup
