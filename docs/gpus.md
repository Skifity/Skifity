# GPUs

An app can be given the GPU in a server: a model server such as Ollama or vLLM,
an image generator such as ComfyUI, a transcription worker. NVIDIA cards are what
this page is mostly about, because they are what nearly everybody has; AMD and
Intel cards work the same way once their vendor's device plugin runs.

Three things have to be true, and only the last two are done from the panel:

1. **The server has the driver and the NVIDIA container toolkit.** These are
   kernel modules and packages on the machine itself. The panel does not install
   them: the right driver depends on the card and the kernel, it usually wants a
   reboot, and a wrong one is a server that does not come back.
2. **The device plugin runs on the server.** It is what tells Kubernetes the
   card exists. **Enable GPUs** on the Servers page installs it.
3. **The app asks for one**, under its settings.

## Setting up a server

### The driver and the container toolkit

On Ubuntu, the driver for a server is installed with Ubuntu's own tool, which
picks the version for the card
([Ubuntu's instructions](https://ubuntu.com/server/docs/how-to/graphics/install-nvidia-drivers/)):

```sh
sudo apt update && sudo apt upgrade
sudo ubuntu-drivers install --gpgpu
sudo reboot
```

After the reboot, `nvidia-smi` lists the card. On Debian, and for a driver of a
particular version, follow
[NVIDIA's driver installation guide](https://docs.nvidia.com/datacenter/tesla/driver-installation-guide/)
instead.

Then the NVIDIA container toolkit, which is how a container reaches the card.
These are NVIDIA's commands for Ubuntu and Debian, from the
[toolkit's installation guide](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html#with-apt-ubuntu-debian):

```sh
sudo apt-get update && sudo apt-get install -y --no-install-recommends \
  ca-certificates curl gnupg2
curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey \
  | sudo gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg
curl -s -L https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list \
  | sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' \
  | sudo tee /etc/apt/sources.list.d/nvidia-container-toolkit.list
sudo apt-get update
export NVIDIA_CONTAINER_TOOLKIT_VERSION=1.20.1-1
sudo apt-get install -y \
  nvidia-container-toolkit=${NVIDIA_CONTAINER_TOOLKIT_VERSION} \
  nvidia-container-toolkit-base=${NVIDIA_CONTAINER_TOOLKIT_VERSION} \
  libnvidia-container-tools=${NVIDIA_CONTAINER_TOOLKIT_VERSION} \
  libnvidia-container1=${NVIDIA_CONTAINER_TOOLKIT_VERSION}
```

The version is the one NVIDIA's guide names as this is written; the guide has
the current one.

Then restart k3s on that server, so it finds the toolkit:

```sh
sudo systemctl restart k3s        # a control-plane server
sudo systemctl restart k3s-agent  # any other
```

Nothing else is configured by hand. k3s looks for the NVIDIA container runtime
when it starts and adds it to its containerd configuration; its RuntimeClass,
`nvidia`, is one k3s always creates. To check it was found, on the server:

```sh
grep nvidia /var/lib/rancher/k3s/agent/etc/containerd/config.toml
```

That is [k3s's own procedure](https://docs.k3s.io/advanced#nvidia-container-runtime).
Do not make `nvidia` k3s's default runtime (`--default-runtime`): then every
container on the server starts under it, and an image that sets
`NVIDIA_VISIBLE_DEVICES=all` — every CUDA base image does — sees every card
without having been given one.

### Enabling GPUs

On the **Servers** page, a panel administrator presses **Enable GPUs**. That
installs NVIDIA's device plugin, version 0.20.1, pinned by digest, on every
server that has an NVIDIA card. It is also under **Settings → Components** as
**NVIDIA GPUs**, where an upgrade is offered when a newer panel pins a newer one.

"Every server that has an NVIDIA card" is decided by labels on the server, in
this order:

* **Node Feature Discovery**, if it runs in the cluster, labels a server with an
  NVIDIA PCI device `feature.node.kubernetes.io/pci-10de.present` (or, with its
  default settings, `pci-0300_10de` or `pci-0302_10de`). Nothing to do.
* **GPU feature discovery**, part of NVIDIA's GPU Operator, labels it
  `nvidia.com/gpu.present`.
* Otherwise nothing in Kubernetes can see a card before the plugin runs, so an
  administrator says so: **This server has an NVIDIA GPU** on the server's page.
  That puts the label `skifity.com/gpu=nvidia` on it.

The plugin is kept to those servers on purpose. It runs under the `nvidia`
runtime, and on a server without the toolkit there is no such runtime: the plugin
would sit in `ContainerCreating` there for ever.

What it is allowed to do, all of which is NVIDIA's own manifest: it is not
privileged, drops every capability, cannot gain privileges, and runs under the
container runtime's default seccomp profile. It needs one thing from the server,
the kubelet's `/var/lib/kubelet/device-plugins` directory, where it registers
with the kubelet. It runs in `kube-system`, beside k3s's own node agents,
because that path is a host path, and the panel's own namespace refuses host
paths.

### What the Servers page shows

For each server, its GPUs: how many, the model and its memory when GPU feature
discovery says, and how many are in use — the sum of what the apps placed on it
asked for, which is the number Kubernetes counts before it places another.

A server with an NVIDIA card that Kubernetes cannot use yet says what is
missing: the plugin is not installed, or it is and cannot start there, with the
reason. Where no tool can see the card, it says that too, and offers to mark the
server.

## Asking for GPUs

On the app's **Settings** tab, under **GPUs**: how many for each instance, whose
cards, and optionally a model to prefer. The same from the CLI:

```sh
skifity gpus                                   # what the app has, and what the cluster offers
skifity gpus --count 1                         # one NVIDIA card for each instance
skifity gpus --count 1 --product NVIDIA-A10    # preferring that model
skifity gpus --count 1 --on web,worker         # the app and its worker process
skifity gpus --count 0                         # none
```

and from an AI assistant, with the `set_gpus` tool.

* **A GPU is not shared.** An instance given one has it to itself, and nothing
  else in the cluster is placed on it. An app with three instances and one GPU
  each needs three cards.
* **All of an instance's cards are in one server.** Asking for more than any one
  server has is refused.
* **A kind of card no server offers is refused**, rather than saved and left
  waiting for a server for ever.
* **Which workloads get them** is yours to say: the app itself (`web`), any of
  its [processes](concepts.md#processes), or both. A worker doing the inference
  can have the card while the web in front of it does not.
* **A model to prefer** is only a preference: with none of that model free, the
  app goes to another card of the same vendor rather than waiting. It is the
  model as GPU feature discovery labels a server with it, such as `NVIDIA-A10`,
  and the panel offers the ones it has seen.
* **Autoscaling** is allowed. If the most instances it can reach, times the cards
  each, is more than the cluster has, the panel warns: the instances past that
  wait for a card to come free.

It is a runtime setting. Changing it restarts the app, and never rebuilds it,
the same as a variable.

Templates can use a GPU as well: the Ollama template asks for one NVIDIA card,
and is given it when a server offers one. On a cluster with none it is installed
without, and runs its models on the CPU; the install says which of its apps
could have had one.

## What an app with a GPU cannot do

* **Scale to zero.** A sleeping app gives its card back, and whatever takes it
  is never asked to return it when a request comes to wake this one; that
  request would wait for a card that does not come free. Giving the GPU to the
  app's processes only is allowed, because processes never sleep.
* **Take its GPU into a preview.** Previews run without one, so a preview never
  holds the card production needs for its next deploy.
* **Give one to a command.** A release command, a one-off command and a
  scheduled command run without a GPU. A release command runs while the version
  before it still holds the card; asking for one there would wait for it, and
  the deploy with it.
* **Deploy without a moment's downtime, with one instance.** A new instance
  needs the card the old one holds, so the old one stops first and the new one
  starts after it. An app with several instances is replaced one at a time and
  keeps serving; an app with one is unavailable while the new version starts.
* **Choose which card.** `NVIDIA_VISIBLE_DEVICES` is set by the device plugin to
  the cards the instance was given, and a variable of the same name is refused:
  set in an app's variables, it would come after the plugin's and hand the app
  every card on the server.
* **A build never has a GPU.** What is in an image does not depend on it.

## When it does not work

### A device plugin is already installed

The panel will not install its device plugin beside another one — one applied
from NVIDIA's manifest, or the GPU Operator's. Two plugins for one card take
turns being the one Kubernetes listens to. Nothing needs doing: apps can use
the cards that plugin offers. Remove it first only if you want the panel to look
after the plugin instead.

### Nothing is offered on a server that has a card

The server's page says which of these it is:

| What it says | What to do |
| --- | --- |
| No device plugin | **Enable GPUs** on the Servers page. |
| The plugin is not on this server | Nothing says the server has a card: mark it with **This server has an NVIDIA GPU**. |
| `no runtime for "nvidia" is configured` | The container toolkit is not installed, or k3s was started before it was. Install it, and restart k3s on that server. |
| The plugin keeps restarting, and says it could not load NVML | The driver is missing or does not match the kernel. `nvidia-smi` on the server says which. |

See also [an app with a GPU does not start](troubleshooting.md#an-app-with-a-gpu-does-not-start).
