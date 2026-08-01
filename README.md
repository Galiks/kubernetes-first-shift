# Kubernetes first shift

## Start Date
28.07.26

## kubectl version
Client Version: v1.36.3
Kustomize Version: v5.8.1

## minicube version
minikube version: v1.38.1
commit: c93a4cb9311efc66b90d33ea03f75f2c4120e9b0

## docker version
Client:
 Version:           29.1.3
 API version:       1.52
 Go version:        go1.24.13
 Git commit:        29.1.3-0ubuntu4.1
 Built:             Wed Apr 29 16:40:20 2026
 OS/Arch:           linux/amd64
 Context:           default

Server:
 Engine:
  Version:          29.1.3
  API version:      1.52 (minimum version 1.44)
  Go version:       go1.24.13
  Git commit:       29.1.3-0ubuntu4.1
  Built:            Wed Apr 29 16:40:20 2026
  OS/Arch:          linux/amd64
  Experimental:     false
 containerd:
  Version:          2.2.2
  GitCommit:        
 runc:
  Version:          1.4.0-0ubuntu1
  GitCommit:        
 docker-init:
  Version:          0.19.0
  GitCommit:        


## FAQ
### Почему Kubernetes Secret не равен зашифрованному хранилищу секретов  
Secret в etcd хранится лишь в base64, не зашифрован по умолчанию; любой с доступом к API может его прочитать; нет rotation, audit, expiry как в Vault/KMS.

### Почему реальные секреты нельзя бездумно хранить в git 
Git хранит историю изменений. Любое изменение можно увидеть - даже если секрет стёрся, то его можно увидеть в истории.