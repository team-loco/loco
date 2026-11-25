package advisorylock

const (
	SourceSweep          int64 = 0x6c6f636f0001
	ImageSweep           int64 = 0x6c6f636f0002
	InstallWebhookSync   int64 = 0x6c6f636f0003
	RegistrySweep        int64 = 0x6c6f636f0004
	PlacementWindowSweep int64 = 0x6c6f636f0005
	ClusterSilenceSweep  int64 = 0x6c6f636f0006
)

// BuildDigests is the lock class that orders a build's success against the image sweeper deleting its digests.
const BuildDigests int32 = 0x6c6f0001
