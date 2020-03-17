# Verifiable Builds

Tekton supports a notion of `Verifiable Builds`, in which the Tekton controller produces
and signs a `manifest` describing every action it takes.
This `manifest` contains a description of everything that happens during a `TaskRun` or
`PipelineRun` that can be used later to verify how an artifact was produced.

The data included in this manifest includes:

* All the inputs to each `TaskRun`
* All parameters used in a `TaskRun`
* All the outputs produced by a `TaskRun`
* All the containers used as `Steps` inside a Task.

Where possible these fields include full content-addressable references, like a hash,
digest or git commit.

This `manifest` is then signed by Tekton controller, and stored as annotation on the
`TaskRun` or `PipelineRun` after the run completes.

The signature is a `PGP` detached signature.
The private key used to sign this payload must be created manually and then stored in
the `signing-secrets` `Secret`, in the `tekton-pipelines` namespace.

## Usage

To get started, you first have to generate a GPG keypair to be used by your Tekton system.
There are many ways to go about this, but you can usually use something like this:

```shell
gpg gen-key
```

Enter a passprase (make sure you remember it!) and a name for the key.

Next, you'll need to upload the private key as a Kubernetes `Secret` so Tekton can use it
to sign.
To do that, export the secret key and base64 encode it:

```shell
gpg --export-secret-key --armor $keyname | base64
```

And set that as the key `private` in the `Secret` `signing-secrets`:

```shell
kubectl edit secret signing-secrets -n tekton-pipelines
```

Do the same for your passphrase (remembering to base64 encode it), setting that as the key
`passphrase`.

## Verification

Assuming you have the keys loaded into GPG on your system (you should if you created them earlier),
you can retrieve the signature and payload using kubectl to verify them.

They are stored in annotations on the `TaskRun`.

```shell
kubectl get taskrun $taskrun -o=json | jq -r .metadata.annotations.body | base64 -D > body
kubectl get taskrun $taskrun -o=json | jq -r .metadata.annotations.signature > signature
```

Then verify them again with gpg:

```shell
gpg --verify signature body
```
