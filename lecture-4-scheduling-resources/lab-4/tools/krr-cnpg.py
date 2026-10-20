import asyncio
import runpy

from robusta_krr.core.integrations.kubernetes import ClusterLoader
from robusta_krr.core.models.objects import K8sObjectData, PodData
from robusta_krr.core.models.allocations import ResourceAllocations

original_list = ClusterLoader.list_scannable_objects
original_pods = ClusterLoader.list_pods


async def list_with_cnpg(self):
    objects = await original_list(self)

    loop = asyncio.get_running_loop()
    pods = await loop.run_in_executor(
        self.executor,
        lambda: self.core.list_namespaced_pod(
            namespace="shop",
            label_selector="cnpg.io/cluster=shop-db",
        ).items,
    )

    for pod in pods:
        for container in pod.spec.containers:
            if container.name != "postgres":
                continue

            # В этой версии kind ограничен Literal стандартных workloads.
            # construct сохраняет реальный kind Cluster для локального адаптера.
            obj = K8sObjectData.construct(
                cluster=self.cluster,
                namespace=pod.metadata.namespace,
                name=pod.metadata.name,
                kind="Cluster",
                container=container.name,
                allocations=ResourceAllocations.from_container(container),
                hpa=None,
                pods=[PodData(name=pod.metadata.name, deleted=False)],
                warnings=set(),
            )
            obj._api_resource = pod
            objects.append(obj)

    return objects


async def list_cnpg_pods(self, obj):
    if obj.kind == "Cluster":
        loop = asyncio.get_running_loop()
        pod = await loop.run_in_executor(
            self.executor,
            lambda: self.core.read_namespaced_pod(
                name=obj.name,
                namespace=obj.namespace,
            ),
        )
        return [PodData(name=pod.metadata.name, deleted=False)]
    return await original_pods(self, obj)


ClusterLoader.list_scannable_objects = list_with_cnpg
ClusterLoader.list_pods = list_cnpg_pods

runpy.run_path("/app/krr.py", run_name="__main__")
