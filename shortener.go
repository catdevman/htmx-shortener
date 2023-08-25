// main.go
package main

import (
	"context"
	"fmt"
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

    fiberdb "github.com/gofiber/storage/dynamodb/v2"
	fibersession "github.com/gofiber/fiber/v2/middleware/session"
	// "time"

	"github.com/gofiber/template/html/v2"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

var app *fiber.App
var fiberLambda *fiberadapter.FiberLambda

type ShortenedURL struct {
    Id string `dynamodbav:"pk"`
    Type string `dynamodbav:"sk"`
    FullURL string `dynamodbav:"full_url" form:"url"`
}

var defaultDDBOptions = func(o *dynamodb.Options) {
    if os.Getenv("AWS_LAMBDA_RUNTIME_API") == "" {
        o.EndpointResolver = dynamodb.EndpointResolverFromURL("http://localhost:8000")
    }
}

func init() {
	log.Printf("Fiber cold start")
    //engine := jet.New("./views", ".jet")
    engine := html.New("./views", ".html")
    store := fiberdb.New(fiberdb.Config{
        Endpoint: "http://localhost:8000",
        WaitForTableCreation: aws.Bool(true),
    })
	app = fiber.New( fiber.Config{
        Views: engine,
    })
    sessConfig := fibersession.Config{
        Storage: store,
    }

//     // create session handler
    sessions := fibersession.New(sessConfig)

    goth_fiber.SessionStore = sessions

    goth.UseProviders(
		google.New(os.Getenv("OAUTH_KEY"), os.Getenv("OAUTH_SECRET"), "http://127.0.0.1:8989/auth/callback/google"),
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

		return ctx.SendString("logout")
	})


	app.Get("/", func(c *fiber.Ctx) error {
        state := goth_fiber.GetState(c)
        fmt.Println("/", "state", state)
        sess, err := goth_fiber.SessionStore.Get(c)
        sessUser := sess.Get("user")
        fmt.Println(fmt.Sprintf("%+v", sessUser))
        user, ok := sessUser.(goth.User)
        if !ok {
            user = goth.User{
                Email: "guest",
            }
        }
        domains := []ShortenedURL{}
        cfg, err := config.LoadDefaultConfig(context.TODO())
        if err != nil {
            panic(err)
        }

        svc := dynamodb.NewFromConfig(cfg, defaultDDBOptions)

        response, err := svc.Scan(context.TODO(), &dynamodb.ScanInput{TableName: aws.String("test")})
        if err != nil {
            panic(err)
        }
        err = attributevalue.UnmarshalListOfMaps(response.Items, &domains)
        if err != nil {
            log.Printf("Couldn't unmarshal query response. Here's why: %v\n", err)
        }
        return c.Render("index", fiber.Map{
            "User": user,
            "Domains": domains,
        }, "layouts/main")
	})

    app.Get("/:id", func(c *fiber.Ctx) error {
        id := c.Params("id")

        cfg, err := config.LoadDefaultConfig(context.TODO())
        if err != nil {
            panic(err)
        }

        svc := dynamodb.NewFromConfig(cfg, defaultDDBOptions)
        domain := ShortenedURL{}
        pk, _ := attributevalue.Marshal(id)
        sk, _ := attributevalue.Marshal("url")
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
        sURL := new(ShortenedURL)
        if err := c.BodyParser(sURL); err != nil{
            return err
        }
        sURL.Type = "url"
        sURL.Id = generateId()
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
        id := c.Params("id")

        cfg, err := config.LoadDefaultConfig(context.TODO())
        if err != nil {
            panic(err)
        }

        svc := dynamodb.NewFromConfig(cfg, defaultDDBOptions)
        pk, _ := attributevalue.Marshal(id)
        sk, _ := attributevalue.Marshal("url")
        svc.DeleteItem(context.TODO(), &dynamodb.DeleteItemInput{
            TableName: aws.String("test"),
            Key: map[string]types.AttributeValue{
                "pk": pk,
                "sk": sk,
            },
        })
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
