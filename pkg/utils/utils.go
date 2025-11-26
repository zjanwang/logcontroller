package utils

import (
	docker "github.com/fsouza/go-dockerclient"
	"gopkg.in/yaml.v3"
	"regexp"
	"strings"
)

func GetContainerIDByPod(containerString string) string {
	var s string
	parts := strings.Split(containerString, "://")
	if len(parts) > 1 {
		s = parts[1]
	}
	return s
}

func GetDockerContainerByID(id string) (*docker.Container, error) {
	dockerClient, err := docker.NewClientFromEnv()
	if err != nil {
		return nil, err
	}
	container, err := dockerClient.InspectContainerWithOptions(docker.InspectContainerOptions{ID: id})
	if err != nil {
		if strings.Contains(err.Error(), "No such container:") {
			return nil, nil
		}
		return nil, err
	}
	return container, nil
}

func ParsePath(envs []*ContainerEnv, path string) string {
	split := strings.Split(path, "/")
	for i := range split {
		re := regexp.MustCompile(`^\$\((.*?)\)$`)
		matches := re.FindAllStringSubmatch(split[i], -1)
		if !(len(matches) > 0 && len(matches[0]) > 0) {
			continue
		}
		matchEnv := matches[0][1]
		for _, env := range envs {
			if env.Key == matchEnv {
				split[i] = env.Value
				break
			}
		}
	}
	res := strings.Join(split, "/")
	return res
}

func ParseEnv(envs []*ContainerEnv, v string) string {
	re := regexp.MustCompile(`^\$\((.*?)\)$`)
	matches := re.FindAllStringSubmatch(v, -1)
	if !(len(matches) > 0 && len(matches[0]) > 0) {
		return v
	}
	matchEnv := matches[0][1]
	for _, env := range envs {
		if env.Key == matchEnv {
			return env.Value
		}
	}
	return v
}

func MergeDuplicateKeys(node *yaml.Node) (*yaml.Node, error) {
	switch node.Kind {
	case yaml.DocumentNode:
		for i, child := range node.Content {
			mergedChild, err := MergeDuplicateKeys(child)
			if err != nil {
				return nil, err
			}
			node.Content[i] = mergedChild
		}
		return node, nil
	case yaml.MappingNode:
		mergedMap := make(map[string]*yaml.Node)
		newMapping := &yaml.Node{
			Kind:    yaml.MappingNode,
			Tag:     node.Tag,
			Content: []*yaml.Node{},
		}
		for i := 0; i < len(node.Content); i += 2 {
			keyNode := node.Content[i]
			valueNode := node.Content[i+1]

			mergedValue, err := MergeDuplicateKeys(valueNode)
			if err != nil {
				return nil, err
			}

			key := keyNode.Value
			if existing, found := mergedMap[key]; found {
				if existing.Kind == yaml.SequenceNode && mergedValue.Kind == yaml.SequenceNode {
					existing.Content = append(existing.Content, mergedValue.Content...)
				}
			} else {
				mergedMap[key] = mergedValue
			}
		}

		var order []string
		for i := 0; i < len(node.Content); i += 2 {
			keyNode := node.Content[i]
			key := keyNode.Value
			exists := false
			for _, k := range order {
				if k == key {
					exists = true
					break
				}
			}
			if !exists {
				order = append(order, key)
			}
		}
		for _, key := range order {
			newKey := &yaml.Node{
				Kind:  yaml.ScalarNode,
				Tag:   "!!str",
				Value: key,
			}
			newMapping.Content = append(newMapping.Content, newKey, mergedMap[key])
		}
		return newMapping, nil
	case yaml.SequenceNode:
		for i, elem := range node.Content {
			mergedElem, err := MergeDuplicateKeys(elem)
			if err != nil {
				return nil, err
			}
			node.Content[i] = mergedElem
		}
		return node, nil
	default:
		return node, nil
	}
}
