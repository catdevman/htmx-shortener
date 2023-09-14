# Prereqs
- Docker
- Go
- awslocal
- Air (optional)

# Setup
- Get DDB running locally with localstack
  - `docker compose up`
- Create test DDB table
  - ` awslocal dynamodb create-table --table-name test --attribute-definitions AttributeName=pk,AttributeType=S AttributeName=sk,AttributeType=S --key-schema AttributeName=pk,KeyType=HASH AttributeName=sk,KeyType=RANGE --billing-mode PAY_PER_REQUEST`
- Run App
 - air (optional, this should hot rebuild for view and code changes but it wasn't always working perfectly)
 - go run ./ (from the root of the project)

# Using
- Go to http://127.0.0.1:8989/login/google to login
- After login with Google it should redirect you to 

# TODOs
- Help Screen to show keyboard shortcuts
- Setup keyboard shortcuts for adding url and showing help screen
- Refactor DDB usage and fiber setup
