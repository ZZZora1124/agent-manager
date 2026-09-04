package k8s

type ClusterManager struct {
	Clusters map[string]*Cluster
}

func (c *ClusterManager) GetClusterByName(name string) *Cluster {
	cluster, ok := c.Clusters[name]
	if ok {
		return cluster
	} else {
		return nil
	}
}
