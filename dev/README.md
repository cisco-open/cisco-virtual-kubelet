# Developement Quick Start

!! This dir will likely be removed at some point.  Using as placeholder for now !!

Run this procedure from the the root of this repo (`../`)

For a quickstart workflow you can:

* Install podman/KIND or some form of development k8s environmnet.  Have this as the current context in your `kubeconfig`
* Install the Okteto binary: https://www.okteto.com/docs/get-started/install-okteto-cli/

Deploy the `./dev/dev-deployment.yaml` manifest to your dev k8s environment.  This will create:

* A deployment with one pod with a 'placeholder' container to-be-replaced with an Okteto dev container
* A configmap mounted in said deployment pod with the virtual-kubelet configuration loading a FAKE driver
* A service-account
* Role and rolebindings for the service-account

Deploy to your k8s cluster: `kubectl apply -f ./dev/dev-deployment.yaml`.

The `./dev/okteto.yaml` config should be pointing at the `okteto-dev` deployment you just created.  When you're read do:

`okteto up -f ./dev/okteto.yaml` (from the root of this repo) - accept the defaults and select `Go` for the project type if required.

This should launch an Okteto Golang development environment inside the k8s deployment and sync these local files.  From this shell you should be able to start the cisco-virtual-kubelet:

```
go run ./cmd/virtual-kubelet
```

In another terminal you should now have a new node registered with your K8s development environment.  Label the node so that you can target it for scheduling directly with a test pod:

```
kubectl label node cunningr-dev-node kubernetes.io/hostname=cunningr-dev-node
```

You should now be able to create/delete the test pod in your dev k8s cluster:

```
kubectl apply -f ./dev/test-pod.yaml
```

As you update the code locally Okteto will sync everything to your k8s development containter environment. From the Okteto dev environment you can start/stop the application and watch the cisco-virtual-kubelet logs.  

To clean up simply leave exit the Okteto shell and do `okteto down -f ./dev/okteto.yaml` and delete the node from k8s:

`kubectl delete node cunningr-dev-node`
