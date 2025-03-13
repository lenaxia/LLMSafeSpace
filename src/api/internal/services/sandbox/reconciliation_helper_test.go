<<<<<<< SEARCH
			// Set up fake clientset with test pod
			fakeClient := k8sClient.Clientset().(*fake.Clientset)
			_, err := fakeClient.CoreV1().Pods(tt.sandbox.Status.PodNamespace).Create(context.Background(), tt.pod, metav1.CreateOptions{})
			if err != nil {
				t.Fatalf("Failed to create pod in fake client: %v", err)
			}

			// Set up Kubernetes API expectations
			k8sClient.On("LlmsafespaceV1").Return(llmMock).Once()
			llmMock.On("Sandboxes", "").Return(sandboxInterface).Once()
			sandboxInterface.On("List", mock.Anything).Return(sandboxList, nil).Once()
=======
			// Set up mock clientset
			fakeClient := fake.NewSimpleClientset()
			k8sClient.On("Clientset").Return(fakeClient).Once()
			
			// Create test pod in fake client
			_, err := fakeClient.CoreV1().Pods(tt.sandbox.Status.PodNamespace).Create(context.Background(), tt.pod, metav1.CreateOptions{})
			if err != nil {
				t.Fatalf("Failed to create pod in fake client: %v", err)
			}

			// Set up Kubernetes API expectations
			k8sClient.On("LlmsafespaceV1").Return(llmMock).Once()
			llmMock.On("Sandboxes", "").Return(sandboxInterface).Once()
			sandboxInterface.On("List", mock.Anything).Return(sandboxList, nil).Once()
>>>>>>> REPLACE
