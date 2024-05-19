package main

import (
	"context"
	"log"
	"net/http"
	"sort"

	"github.com/graphql-go/graphql"
	"github.com/graphql-go/handler"
	"github.com/rs/cors"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type TrieNode struct {
	Children  map[rune]*TrieNode
	IsEnd     bool
	State     *State
	Frequency int
}

type State struct {
	Name      string `bson:"name"`
	Code      string `bson:"code"`
	Frequency int    `bson:"frequency"`
}

var root *TrieNode
var client *mongo.Client

func init() {
	root = &TrieNode{
		Children: make(map[rune]*TrieNode),
	}
	initMongoClient()
	loadStatesIntoTrie()
}

func initMongoClient() {
	var err error
	client, err = mongo.NewClient(options.Client().ApplyURI("mongodb://root:example@localhost:27017"))
	if err != nil {
		log.Fatal(err)
	}
	err = client.Connect(context.Background())
	if err != nil {
		log.Fatal(err)
	}
}

func loadStatesIntoTrie() {
	collection := client.Database("statesDB").Collection("states")
	cursor, err := collection.Find(context.Background(), bson.M{})
	if err != nil {
		log.Fatal(err)
	}
	defer cursor.Close(context.Background())

	for cursor.Next(context.Background()) {
		var state State
		if err := cursor.Decode(&state); err != nil {
			log.Fatal(err)
		}
		insert(root, &state)
	}
}

func insert(root *TrieNode, state *State) {
	node := root
	for _, char := range state.Name {
		if node.Children == nil {
			node.Children = make(map[rune]*TrieNode)
		}
		if _, ok := node.Children[char]; !ok {
			node.Children[char] = &TrieNode{
				Children: make(map[rune]*TrieNode),
			}
		}
		node = node.Children[char]
	}
	node.IsEnd = true
	node.State = state
	node.Frequency = state.Frequency
	log.Printf("Inserted state: %s, Code: %s, Frequency: %d", state.Name, state.Code, state.Frequency)
}

func searchAndUpdateFrequency(root *TrieNode, prefix string) []*State {
	node := root
	for _, char := range prefix {
		if node.Children[char] == nil {
			log.Printf("Character %c not found in Trie for prefix %s", char, prefix)
			return nil
		}
		node = node.Children[char]
	}
	log.Printf("Prefix %s found in Trie", prefix)

	results := []*State{}
	collectStates(node, &results)
	sortStatesByFrequency(results)

	for _, state := range results {
		updateFrequency(root, state.Name)
	}

	return results
}

func collectStates(node *TrieNode, results *[]*State) {
	if node == nil {
		return
	}
	if node.IsEnd {
		*results = append(*results, node.State)
	}
	for char, child := range node.Children {
		log.Printf("Traversing child with char %c", char)
		collectStates(child, results)
	}
}

func sortStatesByFrequency(states []*State) {
	sort.Slice(states, func(i, j int) bool {
		return states[i].Frequency > states[j].Frequency
	})
}

func updateFrequency(root *TrieNode, stateName string) {
	node := root
	for _, char := range stateName {
		node = node.Children[char]
	}
	if node != nil && node.IsEnd {
		node.Frequency++
		node.State.Frequency = node.Frequency

		collection := client.Database("statesDB").Collection("states")
		_, err := collection.UpdateOne(
			context.Background(),
			bson.M{"name": stateName},
			bson.M{"$inc": bson.M{"frequency": 1}},
		)
		if err != nil {
			log.Printf("Error updating frequency in MongoDB for state %s: %v", stateName, err)
		} else {
			log.Printf("Updated frequency for state: %s, New Frequency: %d", stateName, node.Frequency)
		}
	}
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
				log.Printf("Searching for: %s", search)
				results := searchAndUpdateFrequency(root, search)
				if results == nil {
					return []State{}, nil
				}
				for _, state := range results {
					log.Printf("Found state: %+v", state)
				}
				return results, nil
			},
		},
	},
})

func main() {
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: queryType,
	})
	if err != nil {
		log.Fatal(err)
	}

	h := handler.New(&handler.Config{
		Schema:   &schema,
		Pretty:   true,
		GraphiQL: true,
	})

	corsHandler := cors.New(cors.Options{
		AllowedOrigins:   []string{"http://localhost:8083"},
		AllowCredentials: true,
	}).Handler(h)

	http.Handle("/graphql", corsHandler)
	log.Println("Server is running on port 8082")
	log.Fatal(http.ListenAndServe(":8082", nil))
}
