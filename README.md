# NamespaceClass
### Overview
NamespaceClass is a new CRD that allows labeling a namespace with a corresponding NamespaceClass name, from which it will automatically inherit a set of resources.
This can be used for easily bootstrapping a namespace with a set of default resources.

At present, a namespace can only belong to one class. A namespace can change classes, which causes all resources belonging
to the old class to be cleaned up. A namespace can also specify a class that doesn't exist, and when the NamespaceClass
is created, it's resources will be applied in everyname space that has a label for it

Any resource type is supported, including other Custom Resources. RBAC for creating NamespaceClasses should be tightly locked down for this reason, as it can allow injecting arbitrary resources that may not normally be allowed. Due to this the operator has * rbac privileges for all API groups and types

Resources can also be added to or removed from a NamespaceClass which will trigger application and/or cleanup in all namespaces that target this class.

At present, patching resources already deployed is not supported aside from metadata such as labels and annotations. 

### How to

Simply label the namespace with the name of the NamespaceClass:
```
namespaceclass.akuity.io/name: <name>
```

### Example
A NamespaceClass takes in a list of inheritedResources which specifies the resource's API, Kind, Metadata, and a template body. Please note that any namespace specified will be ignored.
Each resource template will be deployed in all namespaces that target this class.

```
---
apiVersion: core.akuity.io/v1alpha1
kind: NamespaceClass
metadata:
  name: foo
spec:
  inheritedResources:
    - apiVersion: v1
      kind: ConfigMap
      metadata:
        name: foo-config
      template:
        data:
          key1: "valueNEWNEWNEW"
          key2: "valueNEWNEWNEW"
          environment: "production"
    - apiVersion: rbac.authorization.k8s.io/v1
      kind: Role
      metadata:
        name: foo-viewer-role
      template:
        rules:
        - apiGroups: [""]
          resources: ["pods", "services"]
          verbs: ["get", "list", "watch"]
     #Simple busybox pod
    - apiVersion: v1
      kind: Pod
      metadata:
        name: foo-busybox
      template:
        spec:
          containers:
          - name: busybox
            image: busybox:latest
            command: ["sleep", "3600"]
            resources:
              limits:
                memory: "64Mi"
                cpu: "100m"
---
apiVersion: core.akuity.io/v1alpha1
kind: NamespaceClass
metadata:
  name: bar
spec:
  inheritedResources:
    - apiVersion: v1
      kind: ConfigMap
      metadata:
        name: bar-config
      template:
        data:
          app: "bar-application"
          environment: "development"
          log-level: "debug"
    - apiVersion: rbac.authorization.k8s.io/v1
      kind: Role
      metadata:
        name: bar-admin-role
      template:
        rules:
        - apiGroups: ["apps"]
          resources: ["deployments", "statefulsets"]
          verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
    - apiVersion: v1
      kind: Secret
      metadata:
        name: bar-credentials
      template:
        type: Opaque
        data:
          username: YWRtaW4=  # admin in base64
          password: cGFzc3dvcmQxMjM=  # password123 in base64
---
apiVersion: v1
kind: Namespace
metadata:
  name: testfoo
  labels:
    namespaceclass.akuity.io/name: foo
---
apiVersion: v1
kind: Namespace
metadata:
  name: testbar
  labels:
    namespaceclass.akuity.io/name: bar
```

The above creates two NamespaceClass's, and two namespaces referring to each class. 
