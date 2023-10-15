// main.go
package main

import (
	"context"
	"strings"
	htmltmpl "html/template"
	"log"
	"net/http"
	"os"

	"github.com/markbates/goth"
	"github.com/markbates/goth/providers/google"

	"github.com/shareed2k/goth_fiber"

	"github.com/teris-io/shortid"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	fiberadapter "github.com/awslabs/aws-lambda-go-api-proxy/fiber"
	"github.com/gofiber/fiber/v2"

	fibersession "github.com/gofiber/fiber/v2/middleware/session"
	fiberdb "github.com/gofiber/storage/dynamodb/v2"

	"github.com/gofiber/template/html/v2"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/expression"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

var app *fiber.App
var fiberLambda *fiberadapter.FiberLambda

type ShortenedURL struct {
    UserId string `dynamodbav:"pk"`
    UrlId string `dynamodbav:"sk"`
    FullURL string `dynamodbav:"full_url" form:"url"`
}

var defaultDDBOptions = func(o *dynamodb.Options) {
    if os.Getenv("AWS_LAMBDA_RUNTIME_API") == "" {
        o.EndpointResolver = dynamodb.EndpointResolverFromURL("http://127.0.0.1:8000")
    }
}

func init() {
    engine := html.New("./views", ".html")
    fiberdbConfig := fiberdb.Config{WaitForTableCreation: aws.Bool(true),}
    if os.Getenv("AWS_LAMBDA_RUNTIME_API") == "" {
        fiberdbConfig.Endpoint = "http://127.0.0.1:8000"
    }
    store := fiberdb.New(fiberdbConfig)
	app = fiber.New(fiber.Config{
        Views: engine,
    })
    sessConfig := fibersession.Config{
        Storage: store,
    }

//     // create session handler
    sessions := fibersession.New(sessConfig)

    goth_fiber.SessionStore = sessions

    goth.UseProviders(
        google.New(os.Getenv("OAUTH_KEY"), os.Getenv("OAUTH_SECRET"), os.Getenv("OAUTH_DOMAIN")),
	)
    app.Get("/login/:provider", goth_fiber.BeginAuthHandler)
    app.Get("/login", goth_fiber.BeginAuthHandler)

    app.Get("/auth/callback/:provider", func(ctx *fiber.Ctx) error {
        user, err := goth_fiber.CompleteUserAuth(ctx, goth_fiber.CompleteUserAuthOptions{ShouldLogout: false})
		if err != nil {
			log.Println("/auth/callback", err)
            return err
		}

        sess, err := goth_fiber.SessionStore.Get(ctx)
        sess.Set("user", user)
        sess.Save()

		return ctx.Redirect("/", http.StatusTemporaryRedirect)

	})
    app.Get("/logout/:provider", func(ctx *fiber.Ctx) error {
		if err := goth_fiber.Logout(ctx); err != nil {
			log.Println(err)
		}

		return ctx.Redirect("/", http.StatusTemporaryRedirect)
	})


	app.Get("/", func(c *fiber.Ctx) error {
        sess, err := goth_fiber.SessionStore.Get(c)
        sessUser := sess.Get("user")
        user, ok := sessUser.(goth.User)
        if !ok {
            user = goth.User{
                Email: "guest",
            }
        }
        domains := []ShortenedURL{}
        cfg, err := config.LoadDefaultConfig(context.TODO())
        if err != nil {
            log.Println(err)
            return c.SendString("sucks to suck")
        }

        svc := dynamodb.NewFromConfig(cfg, defaultDDBOptions)
        keyCond := expression.KeyAnd(
            expression.Key("pk").Equal(expression.Value(user.UserID)),
            expression.Key("sk").BeginsWith("id#"),
        )
        expr, err := expression.NewBuilder().WithKeyCondition(keyCond).Build()
        response, err := svc.Query(context.TODO(), &dynamodb.QueryInput{
            TableName: aws.String(os.Getenv("DDB_TABLE")),
            KeyConditionExpression: expr.KeyCondition(),
            ExpressionAttributeNames:  expr.Names(),
            ExpressionAttributeValues: expr.Values(),
        })

        // response, err := svc.Scan(context.TODO(), &dynamodb.ScanInput{TableName: aws.String("test")})
        if err != nil {
            log.Println(err)
            return c.Render("login", fiber.Map{}, "layouts/main")
        }
        err = attributevalue.UnmarshalListOfMaps(response.Items, &domains)
        if err != nil {
            log.Printf("Couldn't unmarshal query response. Here's why: %v\n", err)
        }
        return c.Render("index", fiber.Map{
            "User": user,
            "Domains": domains,
            "LoggedIn": true,
        }, "layouts/main")
	})

    app.Get("/:id", func(c *fiber.Ctx) error {
        sess, err := goth_fiber.SessionStore.Get(c)
        sessUser := sess.Get("user")
        user, ok := sessUser.(goth.User)
        if !ok {
            return c.SendString("Please login before using shortened urls")
        }
        id := c.Params("id")

        cfg, err := config.LoadDefaultConfig(context.TODO())
        if err != nil {
            panic(err)
        }

        svc := dynamodb.NewFromConfig(cfg, defaultDDBOptions)
        domain := ShortenedURL{}
        pk, _ := attributevalue.Marshal(user.UserID)
        sk, _ := attributevalue.Marshal("id#" + id)
        res, err := svc.GetItem(context.TODO(), &dynamodb.GetItemInput{
            TableName: aws.String("test"),
            Key: map[string]types.AttributeValue{
                "pk": pk,
                "sk": sk,
            },
        })
        attributevalue.UnmarshalMap(res.Item, &domain)
        return c.Redirect(domain.FullURL, http.StatusMovedPermanently)
    })
    app.Post("/add-url", func(c *fiber.Ctx) error {
        sess, err := goth_fiber.SessionStore.Get(c)
        sessUser := sess.Get("user")
        user, ok := sessUser.(goth.User)
        if !ok {
            return c.SendStatus(http.StatusUnauthorized)
        }
        sURL := new(ShortenedURL)
        if err := c.BodyParser(sURL); err != nil{
            return err
        }
        sURL.UserId = user.UserID
        sURL.UrlId = "id#" + generateId()
        cfg, err := config.LoadDefaultConfig(context.TODO())
        if err != nil {
            panic(err)
        }

        svc := dynamodb.NewFromConfig(cfg, defaultDDBOptions)
        i, err := attributevalue.MarshalMap(sURL)
        if err != nil {
            return err
        }
        svc.PutItem(context.TODO(), &dynamodb.PutItemInput{
            TableName: aws.String("test"),
            Item: i,
        })
        tmpl, err := htmltmpl.ParseGlob("./views/*.html")
        return tmpl.ExecuteTemplate(c, "url-list-item", sURL)
    })

    app.Delete("/delete-url/:id", func(c *fiber.Ctx) error {
        sess, err := goth_fiber.SessionStore.Get(c)
        sessUser := sess.Get("user")
        user, ok := sessUser.(goth.User)
        if !ok {
            return c.SendStatus(http.StatusUnauthorized)
        }
        id := c.Params("id")

        cfg, err := config.LoadDefaultConfig(context.TODO())
        if err != nil {
            panic(err)
        }

        svc := dynamodb.NewFromConfig(cfg, defaultDDBOptions)
        pk, _ := attributevalue.Marshal(user.UserID)
        sk, _ := attributevalue.Marshal("id#" + id)
        _, err = svc.DeleteItem(context.TODO(), &dynamodb.DeleteItemInput{
            TableName: aws.String("test"),
            Key: map[string]types.AttributeValue{
                "pk": pk,
                "sk": sk,
            },
        })

        if err != nil {
            return c.SendStatus(http.StatusInternalServerError)
        }

        return c.SendString("")
    })

	fiberLambda = fiberadapter.New(app)
}

// Handler will deal with Fiber working with Lambda
func Handler(ctx context.Context, req events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	// If no name is provided in the HTTP request body, throw an error
	return fiberLambda.ProxyWithContext(ctx, req)
}

func main() {
	// Make the handler available for Remote Procedure Call by AWS Lambda
    if os.Getenv("AWS_LAMBDA_RUNTIME_API") != "" {
    	lambda.Start(Handler)
    } else {
        log.Fatal(app.Listen(":8989"))
    }
}

func generateId() string {
    return shortid.MustGenerate()
}

func (su *ShortenedURL) ParseId() string {
    s := strings.Split(su.UrlId, "#")
    if len(s) > 1 {
        return s[1]
    }
    return s[0]
}
