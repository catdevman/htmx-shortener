package main

import (
	"context"
	htmltmpl "html/template"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

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
    if os.Getenv("AWS_LAMBDA_FUNCTION_NAME") == "" {
        o.BaseEndpoint = aws.String("https://127.0.0.1:8000")
        o.EndpointResolver = dynamodb.EndpointResolverFromURL("http://127.0.0.1:8000")
    }
}

func init() {
    // This is all in the init for the benefit of aws lambda
    engine := html.New("./views", ".html")
    fiberdbConfig := fiberdb.Config{WaitForTableCreation: aws.Bool(true),}
    if os.Getenv("AWS_LAMBDA_FUNCTION_NAME") == "" {
        fiberdbConfig.Endpoint = "http://127.0.0.1:8000"
    }
    store := fiberdb.New(fiberdbConfig)

	app = fiber.New(fiber.Config{
        Views: engine,
    })
    sessConfig := fibersession.Config{
        Storage: store,
    }

    // create session handler
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
    app.Get("/static", func(c *fiber.Ctx) error {
        return c.SendString("This is a static string.. to just test out what is going on")
    })


	app.Get("/", func(c *fiber.Ctx) error {
        sess, err := goth_fiber.SessionStore.Get(c)
        if err != nil {
            log.Println(err)
            panic(err)
        }
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
            return c.SendString("sucks to suck; could not setup aws configuration")
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
        if err != nil {
            log.Println(err)
            panic(err)
        }
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
            TableName: aws.String(os.Getenv("DDB_TABLE")),
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
            TableName: aws.String(os.Getenv("DDB_TABLE")),
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
            log.Println(err)
            panic(err)
        }

        svc := dynamodb.NewFromConfig(cfg, defaultDDBOptions)
        pk, _ := attributevalue.Marshal(user.UserID)
        sk, _ := attributevalue.Marshal("id#" + id)
        _, err = svc.DeleteItem(context.TODO(), &dynamodb.DeleteItemInput{
            TableName: aws.String(os.Getenv("DDB_TABLE")),
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
func Handler(req events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
    //TODO: I believe I found a bug with the fiber lambda proxy when using html instead of json or plain text
    // Issue appears to be with getting the body decoded here (https://github.com/awslabs/aws-lambda-go-api-proxy/blob/master/fiber/adapter.go#L109)
    // and of course this gobbles up the error so I don't know what is actually wrong

    //return fiberLambda.Proxy(req)
    httpReq, err := fiberLambda.ProxyEventToHTTPRequest(req)
    if err != nil {
        return events.APIGatewayProxyResponse{}, err
    }
    resp, err := app.Test(httpReq, 10000)
    if err != nil {
        return events.APIGatewayProxyResponse{}, err
    }

    headers := make(map[string][]string)
    for k, v := range resp.Header {
        headers[k] = v
    }
    
    body, _ := io.ReadAll(resp.Body)
    return events.APIGatewayProxyResponse{
        StatusCode: resp.StatusCode,
        MultiValueHeaders: headers,
        Body: string(body),
        IsBase64Encoded: false,
    }, nil
}

func main() {
	// Make the handler available for Remote Procedure Call by AWS Lambda
    if os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "" {
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
