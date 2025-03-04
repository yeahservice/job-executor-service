package main

import (
	"context"
	"errors"
	v1 "k8s.io/api/core/v1"
	"keptn-sandbox/job-executor-service/pkg/eventhandler"
	"keptn-sandbox/job-executor-service/pkg/k8sutils"
	"log"
	"os"
	"strings"

	cloudevents "github.com/cloudevents/sdk-go/v2" // make sure to use v2 cloudevents here
	"github.com/kelseyhightower/envconfig"
	"github.com/keptn/go-utils/pkg/lib/keptn"
	keptnv2 "github.com/keptn/go-utils/pkg/lib/v0_2_0"
)

var keptnOptions = keptn.KeptnOpts{}
var env envConfig

type envConfig struct {
	// Port on which to listen for cloudevents
	Port int `envconfig:"RCV_PORT" default:"8080"`
	// Path to which cloudevents are sent
	Path string `envconfig:"RCV_PATH" default:"/"`
	// Whether we are running locally (e.g., for testing) or on production
	Env string `envconfig:"ENV" default:"local"`
	// URL of the Keptn configuration service (this is where we can fetch files from the config repo)
	ConfigurationServiceURL string `envconfig:"CONFIGURATION_SERVICE" default:""`
	// The endpoint of the keptn configuration service API
	InitContainerConfigurationServiceAPIEndpoint string `envconfig:"INIT_CONTAINER_CONFIGURATION_SERVICE_API_ENDPOINT" required:"true"`
	// The k8s namespace the job will run in
	JobNamespace string `envconfig:"JOB_NAMESPACE" required:"true"`
	// The token of the keptn API
	KeptnAPIToken string `envconfig:"KEPTN_API_TOKEN"`
	// The init container image to use
	InitContainerImage string `envconfig:"INIT_CONTAINER_IMAGE"`
	// Default resource limits cpu for job and init container
	DefaultResourceLimitsCPU string `envconfig:"DEFAULT_RESOURCE_LIMITS_CPU"`
	// Default resource limits memory for job and init container
	DefaultResourceLimitsMemory string `envconfig:"DEFAULT_RESOURCE_LIMITS_MEMORY"`
	// Default resource requests cpu for job and init container
	DefaultResourceRequestsCPU string `envconfig:"DEFAULT_RESOURCE_REQUESTS_CPU"`
	// Default resource requests memory for job and init container
	DefaultResourceRequestsMemory string `envconfig:"DEFAULT_RESOURCE_REQUESTS_MEMORY"`
}

// ServiceName specifies the current services name (e.g., used as source when sending CloudEvents)
const ServiceName = "job-executor-service"

// DefaultResourceRequirements contains the default k8s resource requirements for the job and initcontainer, parsed on
// startup from env (treat as const)
var /* const */ DefaultResourceRequirements *v1.ResourceRequirements

/**
 * Parses a Keptn Cloud Event payload (data attribute)
 */
func parseKeptnCloudEventPayload(event cloudevents.Event, data interface{}) error {
	err := event.DataAs(data)
	if err != nil {
		log.Fatalf("Got Data Error: %s", err.Error())
		return err
	}
	return nil
}

/**
 * This method gets called when a new event is received from the Keptn Event Distributor
 * Depending on the Event Type will call the specific event handler functions, e.g: handleDeploymentFinishedEvent
 * See https://github.com/keptn/spec/blob/0.2.0-alpha/cloudevents.md for details on the payload
 */
func processKeptnCloudEvent(ctx context.Context, event cloudevents.Event) error {
	// create keptn handler
	log.Printf("Initializing Keptn Handler")
	myKeptn, err := keptnv2.NewKeptn(&event, keptnOptions)
	if err != nil {
		return errors.New("Could not create Keptn Handler: " + err.Error())
	}

	log.Printf("gotEvent(%s): %s - %s", event.Type(), myKeptn.KeptnContext, event.Context.GetID())

	if !strings.Contains(event.Type(), ".triggered") {
		return nil
	}

	eventData := &keptnv2.EventData{}
	err = parseKeptnCloudEventPayload(event, eventData)
	if err != nil {
		log.Printf("failed to convert incoming cloudevent to event data: %v", err)
	}

	eventHandler := &eventhandler.EventHandler{
		Keptn:       myKeptn,
		Event:       event,
		EventData:   eventData,
		ServiceName: ServiceName,
		JobSettings: k8sutils.JobSettings{
			JobNamespace: env.JobNamespace,
			InitContainerConfigurationServiceAPIEndpoint: env.InitContainerConfigurationServiceAPIEndpoint,
			KeptnAPIToken:               env.KeptnAPIToken,
			InitContainerImage:          env.InitContainerImage,
			DefaultResourceRequirements: DefaultResourceRequirements,
		},
	}

	// prevent duplicate events - https://github.com/keptn/keptn/issues/3888
	go eventHandler.HandleEvent()

	return nil
}

/**
 * Usage: ./main
 * no args: starts listening for cloudnative events on localhost:port/path
 *
 * Environment Variables
 * env=runlocal   -> will fetch resources from local drive instead of configuration service
 */
func main() {
	if err := envconfig.Process("", &env); err != nil {
		log.Fatalf("Failed to process env var: %s", err)
	}

	var err error
	DefaultResourceRequirements, err = k8sutils.CreateResourceRequirements(
		env.DefaultResourceLimitsCPU,
		env.DefaultResourceLimitsMemory,
		env.DefaultResourceRequestsCPU,
		env.DefaultResourceRequestsMemory,
	)
	if err != nil {
		log.Fatalf("unable to create default resource requirements: %v", err.Error())
	}

	os.Exit(_main(os.Args[1:], env))
}

/**
 * Opens up a listener on localhost:port/path and passes incoming requets to gotEvent
 */
func _main(args []string, env envConfig) int {

	// configure keptn options
	if env.Env == "local" {
		log.Println("env=local: Running with local filesystem to fetch resources")
		keptnOptions.UseLocalFileSystem = true
	}

	keptnOptions.ConfigurationServiceURL = env.ConfigurationServiceURL

	log.Println("Starting job-executor-service...")
	log.Printf("    on Port = %d; Path=%s", env.Port, env.Path)

	ctx := context.Background()
	ctx = cloudevents.WithEncodingStructured(ctx)

	log.Printf("Creating new http handler")

	// configure http server to receive cloudevents
	p, err := cloudevents.NewHTTP(cloudevents.WithPath(env.Path), cloudevents.WithPort(env.Port))

	if err != nil {
		log.Fatalf("failed to create client, %v", err)
	}
	c, err := cloudevents.NewClient(p)
	if err != nil {
		log.Fatalf("failed to create client, %v", err)
	}

	log.Printf("Starting receiver")
	log.Fatal(c.StartReceiver(ctx, processKeptnCloudEvent))

	return 0
}
