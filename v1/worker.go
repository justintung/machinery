package v1

import (
	"bytes"
	"encoding/json"
	"log"
	"time"

	"github.com/streadway/amqp"
)

// Worker represents a single worker process
type Worker struct {
	app *App
}

// InitWorker - worker constructor
func InitWorker(app *App) *Worker {
	return &Worker{
		app: app,
	}
}

// Launch starts a new worker process
// The worker subscribes to the default queue
// and processes any incoming tasks registered against the app
func (worker *Worker) Launch() {
	log.Printf("Launching a worker with the following settings:")
	log.Printf("- BrokerURL: %s", worker.app.config.BrokerURL)
	log.Printf("- DefaultQueue: %s", worker.app.config.DefaultQueue)

	c := worker.app.NewConnection().Open()
	defer c.Conn.Close()
	defer c.Channel.Close()

	err := c.Channel.Qos(
		3,     // prefetch count
		0,     // prefetch size
		false, // global
	)
	FailOnError(err, "Failed to set QoS")

	deliveries, err := c.Channel.Consume(
		c.Queue.Name, // queue
		"worker",     // consumer
		false,        // auto-ack
		false,        // exclusive
		false,        // no-local
		false,        // no-wait
		nil,          // args
	)
	FailOnError(err, "Failed to register a consumer")

	forever := make(chan bool)

	go func() {
		for d := range deliveries {
			log.Printf("Received new message: %s", d.Body)
			d.Ack(false)
			dotCount := bytes.Count(d.Body, []byte("."))
			t := time.Duration(dotCount)
			time.Sleep(t * time.Second)
			worker.processMessage(&d)
		}
	}()

	log.Printf(" [*] Waiting for messages. To exit press CTRL+C")
	<-forever
}

func (worker *Worker) processMessage(d *amqp.Delivery) {
	s := TaskSignature{}
	json.Unmarshal([]byte(d.Body), &s)

	task := worker.app.GetRegisteredTask(s.Name)
	if task == nil {
		log.Printf("Task with a name '%s' not registered", s.Name)
		return
	}

	// Everything seems fine, process the task!
	log.Printf("Started processing %s", s.Name)
	result, err := task.Run(s.Args, s.Kwargs)

	// Trigger success or error tasks
	worker.finalize(&s, result, err)
}

func (worker *Worker) finalize(s *TaskSignature, result interface{}, err error) {
	if err != nil {
		log.Printf("Failed processing %s", s.Name)
		log.Printf("Error = %v", result)

		for _, errorTask := range s.OnError {
			worker.app.SendTask(&errorTask)
		}
		return
	}

	log.Printf("Finished processing %s", s.Name)
	log.Printf("Result = %v", result)

	for _, successTask := range s.OnSuccess {
		worker.app.SendTask(&successTask)
	}
}