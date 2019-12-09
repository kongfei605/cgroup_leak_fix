++
前提 kubelet已经关闭kmem accounting

++

1. 停止kubelet(替换kubelet为关闭kmem accounting功能的kubelet)
2. 打开/sys/fs/cgroup/memory/kubepods的task migration 开关(虽然kernel doc上说打开目标的task migration即可，我们都打开）
3. 创建新的group /sys/fs/cgroup/memory/kubepods2
4. 将/sys/fs/cgroup/memory/kubepods下的子group对应的memory.limit_in_bytes和tasks 迁移到新的/sys/fs/cgroup/memory/kubepods2
5. 迁移过程中逐步删除各个子group, 直到/sys/fs/cgroup/memory/kubepods被删除，重新创建/sys/fs/cgroup/memory/kubepods
6. 将/sys/fs/cgroup/memory/kubepods2的子group迁移回/sys/fs/cgroup/memory/kubepods
7. 启动kubelet
8. 重启cadvisor
