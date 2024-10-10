<img src="https://anylb.io/images/logo.svg" alt="anylb.io logo"/>

# [anylb.io](https://anylb.io) Kubernetes Agent

[![Build Status](https://img.shields.io/github/workflow/status/anylb.io/k8s-agent/CI?style=flat-square)](https://github.com/anylb.io/k8s-agent/actions)
[![Go Report Card](https://goreportcard.com/badge/github.com/anylb.io/k8s-agent?style=flat-square)](https://goreportcard.com/report/github.com/anylb.io/k8s-agent)
[![License](https://img.shields.io/github/license/anylb.io/k8s-agent?style=flat-square)](https://github.com/anylb.io/k8s-agent/blob/main/LICENSE)
[![Release](https://img.shields.io/github/v/release/anylb.io/k8s-agent?style=flat-square)](https://github.com/anylb.io/k8s-agent/releases)

## What is anylb.io?

Welcome to AnyLB.io, where Kubernetes load balancing is redefined. Our unique service offers provider-agnostic, anycasted load balancers that streamline your Kubernetes operations, no matter where your clusters reside. Say goodbye to the complexities of traditional load balancers and embrace simplicity with AnyLB.io.

## What does anylb.io do?

This Kubernetes agent allows you to connect your Kubernetes cluster to the [anylb.io](https://anylb.io) platform. Once connected, you can interact with your cluster through our AI-powered interface.

## 🚀 Quick Start

1. Visit [anylb.io](https://anylb.io) and add your cluster to your account.
2. Copy the kubectl command provided on the website.
3. Run the command in your terminal to deploy the agent to your cluster:

```bash
kubectl apply -f <URL_provided_on_website>
```

## 🌟 Features

- **Easy Setup**: Deploy the agent to your cluster with a single command
- **Lightweight**: Minimal resource footprint
- **Secure**: Connects your cluster with wireguard to our edges
- **Cross-Cluster Support**: Connect multiple clusters to your anylb.io account
- **Easy to use**: Publish and unpublish your services and/or ingresses to our edge network

## 🛠 Installation

1. Log in to your [anylb.io](https://anylb.io) account.
2. Navigate to the "Add Cluster" section.
3. Follow the on-screen instructions to generate a unique kubectl command for your cluster.
4. Run the provided kubectl command in your terminal to deploy the agent.

## 📄 License

This project is licensed under the BSD 3-Clause License - see the [LICENSE](LICENSE) file for details.

## 🙋‍♀️ Support

- 🐛 [Issue Tracker](https://github.com/anylb.io/k8s-agent/issues)

---

<p align="center">Made with ❤️ by the anylb.io team</p>
