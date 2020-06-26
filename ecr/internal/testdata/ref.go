package testdata

const (
	// FakeRegion is a valid, but fake, region identifier that's present in
	// FakeRef.
	FakeRegion = "is-fake-1"
	// FakeRegistryID is a valid, but fake, registry ID that's present in
	// FakeRef.
	FakeRegistryID = "12345689012"
	// FakeAccountID is a valid, but fake, registry ID that's present in
	// FakeRef.
	FakeAccountID = FakeRegistryID
	// FakeRepository is a valid, but fake, repository name that's present in
	// FakeRef.
	FakeRepository = "example/repo-name"
	// FakeTag is an image tag that's present in FakeRef.
	FakeImageTag = "latest"
	// FakeObject is the Object part that's present in FakeRef.
	FakeObject = ":" + FakeImageTag
	// FakeARN is the composite ARN using Fake testdata components.
	FakeARN = "arn:aws:ecr:" + FakeRegion + ":" + FakeRegistryID + ":repository/" + FakeRepository
	// FakeRef is the composite ECR reference using Fake testdata components
	// that may be used in testing.
	FakeRef = "ecr.aws/" + FakeARN + FakeObject
)

// FakeRefWithObject produces a fake ref with a specified object part.
func FakeRefWithObject(object string) string {
	return "ecr.aws/" + FakeARN + object
}
