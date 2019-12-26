package main

import (
	"bufio"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	memcgRoot          = "/sys/fs/cgroup/memory"
	cgroupMigrationCmd = `cd %s && find . -type f -name "memory.move_charge_at_immigrate"  |while  read file
do
    echo 1 > ${file}
done`
)

var (
	originPath = filepath.Join(memcgRoot, "kubepods")
	transPath  = filepath.Join(memcgRoot, "kubepods2")
)

type (
	Node struct {
		value   interface{}
		child   *Node
		sibling *Node
	}
)

func execCommandWithRetry(cmd string, retry, sleep int) (output string, err error) {
	for i := 0; i < retry; i++ {
		output, err = execCommand(cmd)
		if err == nil {
			return "", nil
		}
		if sleep > 0 {
			time.Sleep(time.Duration(sleep) * time.Millisecond)
		}
	}
	return output, err
}

func execCommand(cmd string) (string, error) {
	c := exec.Command("/bin/sh", "-c", cmd)
	output, err := c.CombinedOutput()
	if err != nil {
		return string(output), err
	}
	return "", nil
}

func checkSlabInfo(dir string) bool {
	cmdStr := fmt.Sprintf("cat %s/memory.kmem.slabinfo", dir)
	cmd := exec.Command("/bin/bash", "-c", cmdStr)
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Fatalln(string(output), err)
	}

	if strings.Contains(string(output), "Input/output error") {
		return false
	}
	return true
}

func openCgroupMigration(dir string) (string, error) {
	cmd := fmt.Sprintf(cgroupMigrationCmd, dir)
	return execCommand(cmd)
}

// 目录结构转成二叉树
// LCRS: 左节点孩子右节点兄弟
func buildTree(path string) (*Node, error) {
	stack := NewStack()
	root := &Node{
		value:   path,
		sibling: nil,
	}
	stack.Push(root)
	for !stack.IsEmpty() {
		cur := stack.Pop().(*Node)
		dirs, _, err := walk(cur.value.(string))
		if err != nil {
			return nil, err
		}
		for i, dir := range dirs {
			c := &Node{
				value: dir,
			}
			if i == 0 {
				cur.child = c
				cur = c
			} else {
				cur.sibling = c
				cur = cur.sibling
			}
			stack.Push(c)
		}
	}
	return root, nil
}

func copyConf(src, dst string) error {
	conf, err := os.Open(src)
	if err != nil {
		log.Printf("open file %s err:%s", src, err)
		return err
	}
	defer conf.Close()
	br := bufio.NewReader(conf)
	for {
		value, _, c := br.ReadLine()
		if c == io.EOF {
			break
		}
		cmdStr := fmt.Sprintf("echo %s >> %s", value, dst)
		cmd := exec.Command("/bin/bash", "-c", cmdStr)
		output, err := cmd.CombinedOutput()
		if err != nil {
			if strings.Contains(string(output), "No such process") {
				continue
			}
			return fmt.Errorf(
				"failed to copy file from %s to %s with %s, output:%s", src, dst, err, string(output))
		}
	}
	return nil
}

func inorderTraverse(root *Node, src, dst string) {
	if root == nil {
		return
	}
	s := NewStack()
	n := root
	for n != nil || !s.IsEmpty() {
		if n != nil {
			s.Push(n)
			n = n.child
		} else {
			e := s.Pop().(*Node)
			n = e.sibling
			// copy conf && copy container && copy proc
			dir := e.value.(string)
			fmt.Println("current group:", dir)
			dstDir := strings.Replace(dir, src, dst, -1)
			if _, err := os.Stat(dstDir); os.IsNotExist(err) {
				fmt.Printf("mkdir %s \n", dstDir)
				err = os.MkdirAll(dstDir, 0755)
				if err != nil {
					log.Printf("mkdir %s error %s\n", dstDir, err)
					continue
				}
			}
			// cgroup migration feature
			cmd := fmt.Sprintf("echo 1 > %s", filepath.Join(dstDir, "memory.move_charge_at_immigrate"))
			output, err := execCommand(cmd)
			if err != nil {
				log.Printf("%s error %s with output\n", cmd, err, output)
				continue
			}

			dirs, files, err := walk(dir)
			if err != nil {
				log.Println(err)
				continue
			}

			for _, file := range files {
				if strings.HasSuffix(file, "memory.limit_in_bytes") ||
					strings.HasSuffix(file, "cgroup.procs") {
					dstFile := strings.Replace(file, src, dst, -1)
					for retry := 0; retry < 3; retry++ {
						err = copyConf(file, dstFile)
						if err == nil {
							break
						}
						log.Println(err)
					}
				}
			}

			if len(dirs) == 0 {
				// drop page cache
				cmd := fmt.Sprintf("echo 0 > %s/memory.force_empty", dir)
				output, err := execCommandWithRetry(cmd, 3, 100)
				if err != nil {
					log.Printf("drop cache  cmd: %s err:%s with output:%s\n", cmd, err, output)
				}

				// clean cgroup
				fmt.Printf("rmdir %s\n", dir)
				cmd = fmt.Sprintf("rmdir %s", dir)
				output, err = execCommandWithRetry(cmd, 3, 100)
				if err != nil {
					log.Printf("rmdir %s err:%s with output:%s\n", dir, err, output)
				}
			}
		}
	}

}

func walk(root string) (dirs, files []string, err error) {
	if len(root) == 0 {
		return nil, nil, fmt.Errorf("%s is nil", root)
	}
	all, err := ioutil.ReadDir(root)
	if err != nil {
		return nil, nil, err
	}
	var objpath string
	dirs = make([]string, 0, len(all))
	files = make([]string, 0, len(all))
	for _, obj := range all {
		objpath = filepath.Join(root, obj.Name())
		if obj.IsDir() {
			dirs = append(dirs, objpath)
		} else {
			files = append(files, objpath)
		}
	}
	return dirs, files, nil
}

func main() {
	if checkSlabInfo(originPath) {
		fmt.Println("this cgroup leaking ,try to fix...")
	} else {
		fmt.Println("this machine cgroup has not been leaking")
		return
	}

	root, err := buildTree(originPath)
	if err != nil {
		log.Printf("build tree for %s err %s\n", originPath, err)
		return
	}

	// stop kubelet
	output, err := execCommand("systemctl stop kubelet")
	if err != nil {
		log.Fatalln(output, err)
	}
	// open cgroup migration feature
	output, err = openCgroupMigration(originPath)
	if err != nil {
		log.Fatalln(output, err)
	}

	// copy limit_in_bytes && cgroup.procs
	inorderTraverse(root, originPath, transPath)

	back, err := buildTree(transPath)
	if err != nil {
		log.Printf("build tree for %s err %s\n", transPath, err)
		return
	}

	output, err = openCgroupMigration(transPath)
	if err != nil {
		log.Fatalln(output, err)
	}
	inorderTraverse(back, transPath, originPath)

	// start kubelet
	output, err = execCommand("systemctl start kubelet")
	if err != nil {
		log.Fatalln(output, err)
	}

	// cadvisort restart
	output, err = execCommand("supervisorctl restart cadvisor")
	if err != nil {
		log.Fatalln(output, err)
	}
}
