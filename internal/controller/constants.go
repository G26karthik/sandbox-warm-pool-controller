package controller

const (
	finalizerName           = "sandbox.koordinator.sh/pool-protection"
	poolNameLabel           = "sandbox.koordinator.sh/pool-name"
	poolNamespaceLabel      = "sandbox.koordinator.sh/pool-ns"
	poolStateLabel          = "sandbox.koordinator.sh/pool-state"
	createdAtAnnot          = "sandbox.koordinator.sh/created-at"
	idleSinceAnnot          = "sandbox.koordinator.sh/idle-since"
	assignedToAnnot         = "sandbox.koordinator.sh/assigned-to"
	assignedAtAnnot         = "sandbox.koordinator.sh/assigned-at"
	statePending            = "pending"
	stateIdle               = "idle"
	stateAssigned           = "assigned"
	stateRecycling          = "recycling"
	stateTerminating        = "terminating"
	conditionPoolReady      = "PoolReady"
	conditionPoolDegraded   = "PoolDegraded"
	conditionPoolAtCapacity = "PoolAtCapacity"
)
