package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/graphql-go/graphql"
	"github.com/graphql-go/handler"
	"github.com/rs/cors"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"gopkg.in/redis.v5"
)

var client *mongo.Client
var trie *TrieNode
var redisClient *redis.Client

// State represents a U.S. state
type State struct {
	Name      string `bson:"name"`
	Code      string `bson:"code"`
	Frequency int    `bson:"frequency"`
}

// TrieNode represents each node in a Trie
type TrieNode struct {
	Children   map[rune]*TrieNode
	IsEndOfWord bool
	State       *State
}

// Create a new Trie node
func NewTrieNode() *TrieNode {
	return &TrieNode{Children: make(map[rune]*TrieNode)}
}

// Insert a word into the Trie
func (t *TrieNode) Insert(state *State) {
	node := t
	word := strings.ToLower(state.Name)
	for _, char := range word {
		if _, found := node.Children[char]; !found {
			node.Children[char] = NewTrieNode()
		}
		node = node.Children[char]
	}
	node.IsEndOfWord = true
	node.State = state
}

// Search for a word in the Trie and return all completions
func (t *TrieNode) Search(prefix string) []*State {
	node := t
	prefix = strings.ToLower(prefix)
	for _, char := range prefix {
		if _, found := node.Children[char]; !found {
			return nil
		}
		node = node.Children[char]
	}
	return node.getAllStates()
}

// Retrieve all states from a given node
func (t *TrieNode) getAllStates() []*State {
	var states []*State
	if t.IsEndOfWord {
		states = append(states, t.State)
	}
	for _, child := range t.Children {
		states = append(states, child.getAllStates()...)
	}
	return states
}

var stateType = graphql.NewObject(graphql.ObjectConfig{
	Name: "State",
	Fields: graphql.Fields{
		"name": &graphql.Field{
			Type: graphql.String,
		},
		"code": &graphql.Field{
			Type: graphql.String,
		},
		"frequency": &graphql.Field{
			Type: graphql.Int,
		},
	},
})

var queryType = graphql.NewObject(graphql.ObjectConfig{
	Name: "Query",
	Fields: graphql.Fields{
		"states": &graphql.Field{
			Type: graphql.NewList(stateType),
			Args: graphql.FieldConfigArgument{
				"search": &graphql.ArgumentConfig{
					Type: graphql.String,
				},
			},
			Resolve: func(p graphql.ResolveParams) (interface{}, error) {
				search := p.Args["search"].(string)
				log.Println("Searching for:", search)
				states := trie.Search(search)
				if states == nil {
					return nil, nil
				}
				// Sort states by frequency
				for i := 0; i < len(states); i++ {
					for j := i + 1; j < len(states); j++ {
						if states[i].Frequency < states[j].Frequency {
							states[i], states[j] = states[j], states[i]
						}
					}
				}
				return states, nil
			},
		},
	},
})

func main() {
	var err error
	client, err = mongo.NewClient(options.Client().ApplyURI("mongodb://root:example@localhost:27017"))
	if err != nil {
		log.Fatal(err)
	}
	err = client.Connect(context.Background())
	if err != nil {
		log.Fatal(err)
	}

	redisClient = redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	_, err = redisClient.Ping().Result()
	if err != nil {
		log.Fatal(err)
	}

	trie = NewTrieNode()
	populateTrie()

	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: queryType,
	})
	if err != nil {
		log.Fatal(err)
	}

	h := handler.New(&handler.Config{
		Schema: &schema,
		Pretty: true,
	})

	mux := http.NewServeMux()
	mux.Handle("/graphql", h)

	c := cors.New(cors.Options{
		AllowedOrigins:   []string{"http://localhost:8080"},
		AllowCredentials: true,
	})
	handler := c.Handler(mux)

	log.Fatal(http.ListenAndServe(":8082", handler))
}

func populateTrie() {
	collection := client.Database("statesDB").Collection("states")
	cursor, err := collection.Find(context.Background(), bson.M{})
	if err != nil {
		log.Fatal(err)
	}
	defer cursor.Close(context.Background())

	var states []State
	if err = cursor.All(context.Background(), &states); err != nil {
		log.Fatal(err)
	}

	for _, state := range states {
		trie.Insert(&state)
		log.Println("Inserted state:", state.Name)
	}
}
